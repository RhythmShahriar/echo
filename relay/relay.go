// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package relay lets a client see a captured email in their own inbox
// before launch without breaking the seal: a reader releases one message
// to one address, the address must match the relay's allowlist and the
// message goes out through the agency's own SMTP account over TLS. Every
// release (every refused one too) is logged in the inbox as a message of
// its own, saying who released what to whom and when.
//
//	POST /v1/messages/{id}/release   {"to": "ann@client.example"}
//
// It needs the read token (X-Echo-Token) or a signed-in reader. Nothing
// else in Echo sends mail; without a relay configured, nothing does.
package relay

import (
	"bytes"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/wire"
)

// Config is where releases go and who may receive them.
type Config struct {
	// URL is the outgoing SMTP account: smtp://user:pass@host:587 sends
	// with STARTTLS, smtps://user:pass@host:465 over TLS from the start.
	// Echo never sends in clear, so a server without STARTTLS is refused.
	URL string
	// Allow lists the addresses a release may go to, as globs matched
	// without case: "*@client.example", "ann@example.com". Empty allows
	// none.
	Allow []string
	// From, when set, is the envelope sender (MAIL FROM), for accounts that
	// only send from verified addresses. The message's From header stays.
	From string
	// ReadToken, in X-Echo-Token, may release.
	ReadToken string
	// Session, when set, names a signed-in reader (the inbox UI login).
	Session func(*http.Request) (user string, ok bool)
	// TLS is the client config for the SMTP server; nil trusts the system
	// roots for the URL's host.
	TLS *tls.Config
	// Timeout bounds one send; default 30 seconds.
	Timeout time.Duration
}

// account is a parsed Config.URL. Its String never shows the password.
type account struct {
	implicitTLS bool
	host, port  string
	user, pass  string
}

func (a account) String() string { return net.JoinHostPort(a.host, a.port) }

func parseURL(raw string) (account, error) {
	u, err := url.Parse(raw)
	if err != nil {
		// url's errors quote the whole URL, password included.
		return account{}, errors.New("relay URL: not a URL; want smtp://user:pass@host:587")
	}
	a := account{host: u.Hostname(), port: u.Port()}
	switch u.Scheme {
	case "smtp":
		if a.port == "" {
			a.port = "587"
		}
	case "smtps":
		a.implicitTLS = true
		if a.port == "" {
			a.port = "465"
		}
	default:
		return account{}, fmt.Errorf("relay URL: scheme %q; want smtp:// (STARTTLS) or smtps:// (TLS)", u.Scheme)
	}
	if a.host == "" {
		return account{}, errors.New("relay URL: no host")
	}
	if u.User != nil {
		a.user = u.User.Username()
		a.pass, _ = u.User.Password()
	}
	return a, nil
}

// Handler serves releases from inbox. It fails when the URL doesn't parse,
// so a wrong setting shows when Echo starts, not at the first release.
func Handler(inbox *catcher.Inbox, cfg Config) (http.Handler, error) {
	acct, err := parseURL(cfg.URL)
	if err != nil {
		return nil, err
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	r := &releaser{inbox: inbox, cfg: cfg, acct: acct}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/messages/{id}/release", r.release)
	return mux, nil
}

type releaser struct {
	inbox *catcher.Inbox
	cfg   Config
	acct  account
}

// Allowed reports whether addr matches one of the allow globs.
func Allowed(allow []string, addr string) bool {
	addr = strings.ToLower(strings.TrimSpace(addr))
	for _, g := range allow {
		if ok, _ := path.Match(strings.ToLower(strings.TrimSpace(g)), addr); ok {
			return true
		}
	}
	return false
}

// who returns the reader making r, or "".
func (rl *releaser) who(r *http.Request) string {
	if t := r.Header.Get(wire.TokenHeader); t != "" && rl.cfg.ReadToken != "" &&
		subtle.ConstantTimeCompare([]byte(t), []byte(rl.cfg.ReadToken)) == 1 {
		return "read token"
	}
	if rl.cfg.Session != nil {
		if user, ok := rl.cfg.Session(r); ok && user != "" {
			return user
		}
	}
	return ""
}

type answer struct {
	Released string `json:"released,omitempty"` // the message ID
	To       string `json:"to,omitempty"`
	Log      string `json:"log,omitempty"` // the ID of the release log message
	Error    string `json:"error,omitempty"`
}

func (rl *releaser) release(w http.ResponseWriter, r *http.Request) {
	who := rl.who(r)
	if who == "" {
		writeJSON(w, http.StatusUnauthorized, answer{Error: "releasing needs the read token or a signed-in reader"})
		return
	}
	var body struct {
		To string `json:"to"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, answer{Error: `want {"to": "address"}`})
		return
	}
	to, err := mail.ParseAddress(body.To)
	if err != nil || strings.Contains(body.To, ",") {
		writeJSON(w, http.StatusBadRequest, answer{Error: fmt.Sprintf("%q isn't one email address", body.To)})
		return
	}
	id := r.PathValue("id")
	m, ok := rl.inbox.Get(id)
	if !ok || m.Channel != model.Email {
		writeJSON(w, http.StatusNotFound, answer{Error: fmt.Sprintf("no email %q", id)})
		return
	}
	if !Allowed(rl.cfg.Allow, to.Address) {
		msg := to.Address + " isn't on the relay's allowlist; nothing was sent"
		logID := rl.log(m, who, to.Address, model.Blocked, msg)
		writeJSON(w, http.StatusForbidden, answer{To: to.Address, Log: logID, Error: msg})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), rl.cfg.Timeout)
	defer cancel()
	if err := rl.send(ctx, m, to.Address); err != nil {
		msg := "the relay at " + rl.acct.String() + " didn't take it: " + rl.scrub(err.Error())
		logID := rl.log(m, who, to.Address, model.Failed, msg)
		writeJSON(w, http.StatusBadGateway, answer{To: to.Address, Log: logID, Error: msg})
		return
	}
	logID := rl.log(m, who, to.Address, model.Sent, "")
	writeJSON(w, http.StatusOK, answer{Released: m.ID, To: to.Address, Log: logID})
}

// scrub takes the account's password and user name out of s: SMTP
// servers sometimes echo what they were sent.
func (rl *releaser) scrub(s string) string {
	for _, secret := range []string{rl.acct.pass, rl.acct.user} {
		if len(secret) >= 3 {
			s = strings.ReplaceAll(s, secret, "•••••")
		}
	}
	return s
}

// log records a release attempt in the inbox, outside any run, so it
// never counts in a compare. It is the relay's own outgoing call, not a
// captured email, so Mailpit-style readers (which list email) skip it.
func (rl *releaser) log(m model.Message, who, to string, status model.Status, errText string) string {
	smtpURL := ""
	if status != model.Blocked {
		smtpURL = "smtp://" + rl.acct.String() // never the login
	}
	entry := rl.inbox.Add(model.Message{Run: "release", Action: "release:" + m.ID, Channel: model.HTTP, Provider: "echo-release",
		Event: "release", Method: "SEND", URL: smtpURL, To: []string{to}, Subject: "Released: " + m.Subject, Status: status, Error: errText,
		Headers: map[string]string{"x-echo-release-of": m.ID, "x-echo-released-by": who},
		Text:    fmt.Sprintf("%s released message %s (%q) to %s.", who, m.ID, m.Subject, to),
		Source:  model.Source{Via: "release", Hook: who}})
	return entry.ID
}

// send delivers m to one address through the account.
func (rl *releaser) send(ctx context.Context, m model.Message, to string) error {
	from := rl.cfg.From
	if from == "" {
		if a, err := mail.ParseAddress(m.From); err == nil {
			from = a.Address
		} else {
			return errors.New("the message has no From address; set the relay's from")
		}
	}
	msg := Message(m)
	addr := net.JoinHostPort(rl.acct.host, rl.acct.port)
	tlsCfg := rl.cfg.TLS
	if tlsCfg == nil {
		tlsCfg = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	tlsCfg = tlsCfg.Clone()
	if tlsCfg.ServerName == "" {
		tlsCfg.ServerName = rl.acct.host
	}
	d := &net.Dialer{}
	var conn net.Conn
	var err error
	if rl.acct.implicitTLS {
		conn, err = (&tls.Dialer{NetDialer: d, Config: tlsCfg}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	c, err := smtp.NewClient(conn, rl.acct.host)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer func() { _ = c.Close() }()
	if err := c.Hello("echo.local"); err != nil {
		return err
	}
	if !rl.acct.implicitTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("it doesn't offer STARTTLS; Echo never sends a login or a message in clear")
		}
		if err := c.StartTLS(tlsCfg); err != nil {
			return err
		}
	}
	if rl.acct.user != "" {
		if err := c.Auth(smtp.PlainAuth("", rl.acct.user, rl.acct.pass, rl.acct.host)); err != nil {
			return err
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	wc, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := wc.Write(msg); err != nil {
		_ = wc.Close()
		return err
	}
	if err := wc.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// Message returns the email to send for m: the captured MIME when Echo
// has all of it (with an X-Echo-Release header added and Bcc dropped),
// else one built from the fields (text, HTML or both).
func Message(m model.Message) []byte {
	release := "X-Echo-Release: " + m.ID + "\r\n"
	if raw := m.Body; raw != "" && rawMIME(m) && !strings.HasSuffix(raw, "…(truncated)") {
		return append([]byte(release), dropBcc(crlf(raw))...)
	}
	var b bytes.Buffer
	h := func(k, v string) {
		if v != "" {
			b.WriteString(k + ": " + v + "\r\n")
		}
	}
	h("From", m.From)
	h("To", strings.Join(m.To, ", "))
	h("Cc", strings.Join(m.CC, ", "))
	h("Reply-To", m.ReplyTo)
	h("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	h("Date", m.At.Format(time.RFC1123Z))
	h("Message-ID", "<"+m.ID+".release@echo.local>")
	b.WriteString(release)
	b.WriteString("MIME-Version: 1.0\r\n")
	part := func(ctype, body string) {
		b.WriteString("Content-Type: " + ctype + "; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")
		qp := quotedprintable.NewWriter(&b)
		_, _ = qp.Write([]byte(body))
		_ = qp.Close()
		b.WriteString("\r\n")
	}
	switch {
	case m.Text != "" && m.HTML != "":
		const boundary = "echo-release-alternative"
		b.WriteString("Content-Type: multipart/alternative; boundary=" + boundary + "\r\n\r\n")
		b.WriteString("--" + boundary + "\r\n")
		part("text/plain", m.Text)
		b.WriteString("--" + boundary + "\r\n")
		part("text/html", m.HTML)
		b.WriteString("--" + boundary + "--\r\n")
	case m.HTML != "":
		part("text/html", m.HTML)
	default:
		part("text/plain", m.Text)
	}
	return b.Bytes()
}

// rawMIME reports whether m.Body is the email itself. For mail sent
// through a provider's HTTP API (SendGrid, Mailgun) Body is the API
// request, JSON or a form that may name every recipient, so the email is
// built from the fields instead.
func rawMIME(m model.Message) bool {
	switch m.Source.Via {
	case "smtp", "sendmail":
		return true
	}
	return m.Method == ""
}

func crlf(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
}

// dropBcc removes Bcc header lines (and their continuations) from a raw
// message's header block, which some mailers leave in.
func dropBcc(raw string) string {
	head, body, found := strings.Cut(raw, "\r\n\r\n")
	if !found {
		return raw
	}
	var out []string
	skipping := false
	for _, line := range strings.Split(head, "\r\n") {
		if skipping && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) {
			continue
		}
		skipping = len(line) >= 4 && strings.EqualFold(line[:4], "bcc:")
		if !skipping {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\r\n") + "\r\n\r\n" + body
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}
