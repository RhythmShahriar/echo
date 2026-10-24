// Package sendmail is a sendmail-compatible shim: `rhythm-echo sendmail`
// reads a message on stdin, like /usr/sbin/sendmail, and hands it to Echo
// instead of delivering it. PHP's mail() (through sendmail_path), cron,
// Git and most Unix tools call sendmail, so pointing them here captures
// their mail without touching the app.
//
// Delivery goes to Echo's SMTP sink (RHYTHM_ECHO_SMTP, default
// 127.0.0.1:1025), or to the capture API when only RHYTHM_ECHO_URL is set.
// Nothing is ever delivered for real.
package sendmail

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"os"
	"os/user"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/wire"
)

// Environment variables the shim reads.
const (
	EnvSMTP  = "RHYTHM_ECHO_SMTP"  // host:port of Echo's SMTP sink
	EnvURL   = "RHYTHM_ECHO_URL"   // Echo's API, e.g. http://127.0.0.1:8025
	EnvToken = "RHYTHM_ECHO_TOKEN" //nolint:gosec // a variable name, not a credential; sent as X-Echo-Token
	EnvForce = "RHYTHM_ECHO_FORCE" // "1" lets the shim run where the environment says production

	DefaultSMTP = "127.0.0.1:1025"
)

// Exit codes from sysexits.h, which callers of sendmail understand.
const (
	exOK          = 0
	exUsage       = 64
	exDataErr     = 65
	exUnavailable = 69
	exTempFail    = 75
	exConfig      = 78
)

const maxMessage = 25 << 20

// options are the sendmail flags the shim understands.
type options struct {
	extract    bool // -t: recipients from To, Cc and Bcc
	ignoreDots bool // -i / -oi: a lone "." does not end the message
	from       string
	fullName   string
	rcpts      []string
}

// Main runs the shim with sendmail's command line (without the program
// name) and returns the exit code.
func Main(args []string, stdin io.Reader, stderr io.Writer) int {
	o, err := parseArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "rhythm-echo sendmail: %v\n", err)
		return exUsage
	}
	if env := production(); env != "" && os.Getenv(EnvForce) != "1" {
		fmt.Fprintf(stderr, "rhythm-echo sendmail: refusing to run: %s says production (set %s=1 to force)\n", env, EnvForce)
		return exConfig
	}
	raw, err := readMessage(stdin, o.ignoreDots)
	if err != nil {
		fmt.Fprintf(stderr, "rhythm-echo sendmail: %v\n", err)
		return exDataErr
	}
	msg, from, rcpts, err := prepare(raw, o)
	if err != nil {
		fmt.Fprintf(stderr, "rhythm-echo sendmail: %v\n", err)
		return exDataErr
	}
	if len(rcpts) == 0 {
		fmt.Fprintln(stderr, "rhythm-echo sendmail: no recipients (give them as arguments, or use -t)")
		return exUsage
	}
	smtpAddr, apiURL := os.Getenv(EnvSMTP), os.Getenv(EnvURL)
	if apiURL != "" && smtpAddr == "" {
		err = viaAPI(apiURL, os.Getenv(EnvToken), msg, from, rcpts)
	} else {
		if smtpAddr == "" {
			smtpAddr = DefaultSMTP
		}
		err = viaSMTP(smtpAddr, msg, from, rcpts)
	}
	if err != nil {
		fmt.Fprintf(stderr, "rhythm-echo sendmail: %v\n", err)
		var ne net.Error
		if errors.As(err, &ne) || errors.Is(err, errUnreachable) {
			return exUnavailable
		}
		return exTempFail
	}
	return exOK
}

// parseArgs reads sendmail's flags. Options that only matter to a real
// MTA (-o*, -B, -N, -R, -V, -X, -v, -q, -bm) are accepted and ignored so
// callers that pass them keep working.
func parseArgs(args []string) (options, error) {
	var o options
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			o.rcpts = append(o.rcpts, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			o.rcpts = append(o.rcpts, a)
			continue
		}
		// value takes the rest of the flag ("-ffoo") or the next argument.
		value := func(flag string) (string, error) {
			if len(a) > len(flag) {
				return a[len(flag):], nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", flag)
			}
			i++
			return args[i], nil
		}
		var err error
		switch {
		case a == "-t":
			o.extract = true
		case a == "-i" || a == "-oi":
			o.ignoreDots = true
		case strings.HasPrefix(a, "-f") || strings.HasPrefix(a, "-r"):
			o.from, err = value(a[:2])
		case strings.HasPrefix(a, "-F"):
			o.fullName, err = value("-F")
		case strings.HasPrefix(a, "-B"), strings.HasPrefix(a, "-N"), strings.HasPrefix(a, "-R"),
			strings.HasPrefix(a, "-V"), strings.HasPrefix(a, "-X"), strings.HasPrefix(a, "-L"):
			if len(a) == 2 {
				_, err = value(a)
			}
		case strings.HasPrefix(a, "-o"), a == "-v", a == "-U", a == "-bm", a == "-em", a == "-ep", a == "-eq", a == "-G":
		case strings.HasPrefix(a, "-b"):
			return o, fmt.Errorf("mode %s is not supported (only sending, -bm)", a)
		default:
			return o, fmt.Errorf("unknown option %s", a)
		}
		if err != nil {
			return o, err
		}
	}
	return o, nil
}

// readMessage reads stdin. Without -i a line holding only "." ends the
// message, as in classic sendmail.
func readMessage(r io.Reader, ignoreDots bool) ([]byte, error) {
	lr := io.LimitReader(r, maxMessage+1)
	if ignoreDots {
		b, err := io.ReadAll(lr)
		if err == nil && len(b) > maxMessage {
			err = errors.New("message too large")
		}
		return b, err
	}
	var out bytes.Buffer
	br := bufio.NewReader(lr)
	for {
		line, err := br.ReadBytes('\n')
		if strings.TrimRight(string(line), "\r\n") == "." && bytes.HasSuffix(line, []byte("\n")) {
			break
		}
		out.Write(line)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	if out.Len() > maxMessage {
		return nil, errors.New("message too large")
	}
	return out.Bytes(), nil
}

// prepare works out the envelope and fills headers a real sendmail would
// add (From, Date, Message-ID). With -t it removes Bcc from the message.
func prepare(raw []byte, o options) (msg []byte, from string, rcpts []string, err error) {
	head, body := splitHeader(raw)
	m, perr := mail.ReadMessage(bytes.NewReader(append(append([]byte{}, head...), "\r\n"...)))
	var h mail.Header
	if perr == nil {
		h = m.Header
	} else if len(bytes.TrimSpace(head)) > 0 {
		return nil, "", nil, fmt.Errorf("can't read the message headers: %w", perr)
	}
	rcpts = append(rcpts, o.rcpts...)
	if o.extract {
		for _, k := range []string{"To", "Cc", "Bcc"} {
			rcpts = append(rcpts, addresses(h.Get(k))...)
		}
		head = dropHeader(head, "Bcc")
	}
	rcpts = dedupe(rcpts)

	from = o.from
	if from == "" {
		if a := addresses(h.Get("From")); len(a) > 0 {
			from = a[0]
		}
	}
	if from == "" {
		from = localUser()
	}
	var add bytes.Buffer
	if h.Get("From") == "" {
		if o.fullName != "" {
			add.WriteString("From: " + (&mail.Address{Name: o.fullName, Address: from}).String() + "\r\n")
		} else {
			add.WriteString("From: " + from + "\r\n")
		}
	}
	if h.Get("Date") == "" {
		add.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	}
	if h.Get("Message-Id") == "" {
		b := make([]byte, 12)
		_, _ = rand.Read(b)
		add.WriteString("Message-ID: <" + hex.EncodeToString(b) + "@rhythm-echo.sendmail>\r\n")
	}
	msg = append(append(append(add.Bytes(), head...), "\r\n"...), body...)
	return msg, from, rcpts, nil
}

// splitHeader splits at the first empty line (LF or CRLF). The header
// keeps its final line break.
func splitHeader(raw []byte) (head, body []byte) {
	for i := 0; i < len(raw); {
		j := bytes.IndexByte(raw[i:], '\n')
		if j < 0 {
			return raw, nil
		}
		line := raw[i : i+j]
		if len(bytes.TrimRight(line, "\r")) == 0 {
			return raw[:i], raw[i+j+1:]
		}
		i += j + 1
	}
	return raw, nil
}

// dropHeader removes a header and its continuation lines.
func dropHeader(head []byte, name string) []byte {
	var out bytes.Buffer
	skipping := false
	for _, line := range bytes.SplitAfter(head, []byte("\n")) {
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			if !skipping {
				out.Write(line)
			}
			continue
		}
		k, _, _ := bytes.Cut(line, []byte(":"))
		skipping = strings.EqualFold(strings.TrimSpace(string(k)), name)
		if !skipping {
			out.Write(line)
		}
	}
	return out.Bytes()
}

func addresses(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	list, err := mail.ParseAddressList(v)
	if err != nil {
		// Unparseable (bare names, odd quoting): keep comma-separated parts.
		var out []string
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	out := make([]string, 0, len(list))
	for _, a := range list {
		out = append(out, a.Address)
	}
	return out
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range in {
		k := strings.ToLower(r)
		if r == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, r)
	}
	return out
}

func localUser() string {
	name := "nobody"
	if u, err := user.Current(); err == nil && u.Username != "" {
		name = u.Username
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "localhost"
	}
	return name + "@" + host
}

var errUnreachable = errors.New("echo is not reachable")

func viaSMTP(addr string, msg []byte, from string, rcpts []string) error {
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second) //nolint:gosec // the address is the operator's own Echo
	if err != nil {
		return fmt.Errorf("%w at %s (is `rhythm echo up` running? set %s): %w", errUnreachable, addr, EnvSMTP, err)
	}
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	host, _, _ := net.SplitHostPort(addr)
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer func() { _ = c.Close() }()
	if err := c.Hello("localhost"); err != nil {
		return err
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, r := range rcpts {
		if err := c.Rcpt(r); err != nil {
			return fmt.Errorf("recipient %s: %w", r, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// viaAPI posts the message as an email capture. Envelope recipients not
// in To or Cc are reported as Bcc, like the SMTP sink does.
func viaAPI(base, token string, msg []byte, from string, rcpts []string) error {
	m, err := mail.ReadMessage(bytes.NewReader(msg))
	e := &wire.Email{From: from, Raw: string(msg)}
	run := ""
	if err == nil {
		to, cc := addresses(m.Header.Get("To")), addresses(m.Header.Get("Cc"))
		e.To, e.CC = to, cc
		for _, r := range rcpts {
			if !containsFold(to, r) && !containsFold(cc, r) {
				e.BCC = append(e.BCC, r)
			}
		}
		run = m.Header.Get(wire.RunHeader)
	}
	if len(e.To) == 0 && len(e.CC) == 0 {
		e.To, e.BCC = rcpts, nil
	}
	body, err := json.Marshal(wire.Capture{Version: wire.Version, Kind: "email", Run: run,
		Source: wire.Source{Via: "sendmail", Hook: "sendmail"}, Email: e})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(base, "/")+"/v1/capture", bytes.NewReader(body)) //nolint:gosec // the operator's own Echo
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set(wire.TokenHeader, token)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req) //nolint:gosec // the operator's own Echo
	if err != nil {
		return fmt.Errorf("%w at %s (is `rhythm echo up` running? set %s): %w", errUnreachable, base, EnvURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("echo answered %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return nil
}

func containsFold(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}

// production returns the variable that marks this environment as
// production, if any: the shim would swallow real mail there.
func production() string {
	for _, k := range []string{"WP_ENVIRONMENT_TYPE", "APP_ENV", "VERCEL_ENV", "NODE_ENV"} {
		if strings.EqualFold(os.Getenv(k), "production") {
			return k
		}
	}
	return ""
}
