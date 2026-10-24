// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package catcher

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net"
	netmail "net/mail"
	"path"
	"strings"
	"time"

	"github.com/emersion/go-message"
	"github.com/emersion/go-message/mail"
	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"

	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/wire"
)

// maxMail is the largest message the sink accepts.
const maxMail = 25 << 20

// NewSMTP returns an SMTP server that captures every message it receives
// into s. It never delivers anything, in observe mode too, so its mail is
// recorded as faked: point a site's mailer (or sendmail_path, through
// `rhythm-echo sendmail`) at it on staging.
func NewSMTP(s *Server, addr string) *smtp.Server {
	srv := smtp.NewServer(backend{s})
	srv.Addr = addr
	srv.Domain = "echo.local"
	srv.ReadTimeout = 30 * time.Second
	srv.WriteTimeout = 30 * time.Second
	srv.MaxMessageBytes = maxMail
	srv.MaxRecipients = 500
	srv.AllowInsecureAuth = true // any credentials are accepted: sites keep their SMTP settings
	return srv
}

type backend struct{ s *Server }

func (b backend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	host := ""
	if a, ok := c.Conn().RemoteAddr().(*net.TCPAddr); ok {
		host = a.IP.String()
	}
	return &session{s: b.s, client: host}, nil
}

type session struct {
	s      *Server
	client string
	site   string // set by SetSite: the site whose write token logged in
	from   string
	to     []string
}

// SiteSetter is what the sink's sessions implement, so a backend that
// wraps them and checks logins (the hub, up --sites) can mark what a
// session captures with the site whose write token logged in, as a write
// token does over HTTP.
type SiteSetter interface {
	SetSite(site string)
}

// SetSite marks what this session captures from now on as site's.
func (s *session) SetSite(site string) { s.site = site }

// AuthMechanisms and Auth accept any login, so a site configured for a
// real SMTP provider can point at Echo unchanged.
func (s *session) AuthMechanisms() []string { return []string{sasl.Plain} }
func (s *session) Auth(string) (sasl.Server, error) {
	return sasl.NewPlainServer(func(_, _, _ string) error { return nil }), nil
}

func (s *session) Mail(from string, _ *smtp.MailOptions) error { s.from = from; return nil }
func (s *session) Rcpt(to string, _ *smtp.RcptOptions) error   { s.to = append(s.to, to); return nil }
func (s *session) Reset()                                      { s.from, s.to = "", nil }
func (s *session) Logout() error                               { return nil }

func (s *session) Data(r io.Reader) error {
	raw, err := io.ReadAll(io.LimitReader(r, maxMail+1))
	if err != nil {
		return err
	}
	if len(raw) > maxMail {
		return errors.New("message too large")
	}
	p, _ := ParseMIME(raw)
	e := &wire.Email{From: p.From, To: p.To, CC: p.CC, ReplyTo: p.ReplyTo, Subject: p.Subject, Text: p.Text,
		HTML: p.HTML, Headers: p.Headers, Raw: string(raw)}
	if e.From == "" {
		e.From = s.from
	}
	// Envelope recipients that aren't in To or Cc were Bcc'd.
	for _, rcpt := range s.to {
		if !containsFold(p.To, rcpt) && !containsFold(p.CC, rcpt) {
			e.BCC = append(e.BCC, rcpt)
		}
	}
	if len(e.To) == 0 && len(e.CC) == 0 {
		e.To, e.BCC = s.to, nil
	}
	_, err = s.s.captureFor(wire.Capture{Version: wire.Version, Kind: "email", Run: p.Headers[strings.ToLower(wire.RunHeader)],
		Source: wire.Source{Via: "smtp", Host: s.client, Plugin: mailer(p.Headers)}, Email: e}, extras{}, s.site)
	return err
}

// mailer names the software that built the message, from X-Mailer.
func mailer(h map[string]string) string {
	m := h["x-mailer"]
	if i := strings.IndexAny(m, " ("); i > 0 {
		m = m[:i]
	}
	return m
}

// ParseMIME reads an RFC 5322 message: headers, addresses, subject, the
// first text and HTML parts. It also records the name, type, decoded size
// and SHA-256 of every attachment and named inline part (never their
// content).
func ParseMIME(raw []byte) (model.Message, error) {
	var m model.Message
	r, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		return m, err
	}
	h := r.Header
	m.Headers = map[string]string{}
	fields := h.Fields()
	for fields.Next() {
		k := strings.ToLower(fields.Key())
		if v, err := fields.Text(); err == nil {
			if old, ok := m.Headers[k]; ok {
				v = old + ", " + v
			}
			m.Headers[k] = v
		}
	}
	m.Subject, _ = h.Subject()
	m.From = first(addrs(h, "From"))
	m.To = addrs(h, "To")
	m.CC = addrs(h, "Cc")
	m.ReplyTo = first(addrs(h, "Reply-To"))
	for {
		p, err := r.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		// A part in a charset or encoding Go doesn't know still reads.
		if err != nil && !message.IsUnknownCharset(err) && !message.IsUnknownEncoding(err) {
			return m, err
		}
		h := partHeader(p.Header)
		ct, params, _ := h.ContentType()
		disp, dparams, _ := h.ContentDisposition()
		name := dparams["filename"] // RFC 2231 and RFC 2047 names come decoded
		if name == "" {
			name = params["name"]
		}
		if name == "" && disp != "attachment" && (ct == "" || strings.HasPrefix(ct, "text/")) {
			b, err := io.ReadAll(io.LimitReader(p.Body, maxMail))
			if err != nil {
				return m, err
			}
			switch {
			case ct == "text/html" && m.HTML == "":
				m.HTML = string(b)
			case (ct == "text/plain" || ct == "") && m.Text == "":
				m.Text = string(b)
			}
			continue
		}
		a, err := attachment(p.Body, name, ct)
		if err != nil {
			return m, err
		}
		// Shown in the body: said so, or an unnamed part the HTML refers
		// to by its Content-ID (multipart/related).
		a.Inline = disp == "inline" || disp == "" && h.Get("Content-Id") != ""
		m.Attachments = append(m.Attachments, a)
	}
	return m, nil
}

// partHeader returns the header of a mail part, whichever kind it is.
func partHeader(h mail.PartHeader) message.Header {
	switch h := h.(type) {
	case *mail.InlineHeader:
		return h.Header
	case *mail.AttachmentHeader:
		return h.Header
	}
	return message.Header{}
}

// attachment reads body (decoded) for its size and hash.
func attachment(body io.Reader, name, typ string) (model.Attachment, error) {
	sum := sha256.New()
	n, err := io.Copy(sum, io.LimitReader(body, maxMail))
	if err != nil {
		return model.Attachment{}, err
	}
	return model.Attachment{Name: name, Type: typ, Size: n, SHA256: hex.EncodeToString(sum.Sum(nil))}, nil
}

// headerAttachments reads the X-Echo-Attachments header older WordPress
// connectors send in place of the files: their names, comma-separated.
// Only the names are known.
func headerAttachments(v string) []model.Attachment {
	var out []model.Attachment
	for name := range strings.SplitSeq(v, ",") {
		if name = strings.TrimSpace(name); name != "" {
			typ, _, _ := mime.ParseMediaType(mime.TypeByExtension(path.Ext(name)))
			out = append(out, model.Attachment{Name: name, Type: typ})
		}
	}
	return out
}

func addrs(h mail.Header, key string) []string {
	list, err := h.AddressList(key)
	if err != nil {
		if v := h.Get(key); v != "" {
			return []string{v}
		}
		return nil
	}
	out := make([]string, 0, len(list))
	for _, a := range list {
		out = append(out, a.Address)
	}
	return out
}

// sentAt returns when a message says it was written: its Date header.
// Mail without one, or with one that doesn't parse, has no usable time.
func sentAt(h map[string]string) (time.Time, bool) {
	v := h["date"]
	if v == "" {
		return time.Time{}, false
	}
	t, err := netmail.ParseDate(v)
	return t, err == nil
}

func first(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

func containsFold(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}
