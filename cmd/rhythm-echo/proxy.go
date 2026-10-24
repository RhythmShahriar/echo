// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/sha1" //nolint:gosec // fingerprints only, for tools that name certificates by SHA-1
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"runtime"
	"slices"
	"strings"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/proxy"
	"github.com/RhythmShahriar/echo/sendmail"
)

// startProxy serves the HTTP(S) proxy on ln, for apps and SDKs that skip
// the connectors (--proxy, or proxy: in the config). up binds ln with its
// other listeners, so nothing is printed before every port is taken. It
// returns the line up prints for the proxy, notes about the CA (a move,
// a rotation, the host trusting it) and the proxy's shutdown.
func startProxy(s *session, srv *catcher.Server, ln net.Listener) (string, []caNote, func(context.Context), error) {
	cfg := s.cfg
	var ca *proxy.CA
	if cfg.MITM {
		var err error
		if ca, err = proxy.LoadCA(""); err != nil {
			return "", nil, nil, fmt.Errorf("loading the proxy CA: %w", err)
		}
	}
	p, err := proxy.New(proxy.Options{Server: srv, CA: ca, MITM: cfg.MITM, Allow: cfg.Allow, Token: s.echo.Token,
		Log: slog.New(viewHandler{log: s.view.Log})})
	if err != nil {
		return "", nil, nil, err
	}
	go func() {
		if err := p.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.view.Log("proxy: " + err.Error())
		}
	}()
	line := cfg.Proxy + "  (HTTPS_PROXY for SDKs that skip the connector)"
	var notes []caNote
	if cfg.MITM {
		line += "; decrypting HTTPS, trust " + ca.CertPath
		notes = caNotes(ca)
	}
	return line, notes, stopProxy(p), nil
}

// stopProxy stops p: gracefully until ctx is done, then at once.
func stopProxy(p *proxy.Proxy) func(context.Context) {
	return func(ctx context.Context) { _ = p.Shutdown(ctx) }
}

// viewHandler shows the proxy's warnings (a refused payment tunnel) in
// the live log, as "proxy: message key=value".
type viewHandler struct {
	log   func(string)
	attrs []slog.Attr
}

func (viewHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= slog.LevelWarn }

func (h viewHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString("proxy: " + r.Message)
	add := func(a slog.Attr) bool { fmt.Fprintf(&b, " %s=%s", a.Key, a.Value); return true }
	for _, a := range h.attrs {
		add(a)
	}
	r.Attrs(add)
	h.log(b.String())
	return nil
}

func (h viewHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return viewHandler{log: h.log, attrs: append(slices.Clip(h.attrs), as...)}
}

func (h viewHandler) WithGroup(string) slog.Handler { return h }

// cmdCA prints where the proxy's CA certificate is, creating it if needed,
// so it can be trusted in containers and runtimes (--pem prints it).
// --untrust prints how to remove it from the places it is usually added.
func cmdCA(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ca", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	pem := fs.Bool("pem", false, "print the certificate instead of its path")
	untrust := fs.Bool("untrust", false, "print how to stop trusting the CA")
	const usage = "usage: rhythm echo ca [--pem | --untrust]"
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 || (*pem && *untrust) {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stdout, usage+"   (--pem prints the certificate instead of its path; --untrust prints how to remove it)")
			return exitOK
		}
		fmt.Fprintln(stderr, "rhythm echo: "+usage)
		return exitUsage
	}
	if *untrust {
		fmt.Fprint(stdout, untrustSteps(proxy.Installed(), proxy.Dirs()))
		return exitOK
	}
	ca, err := proxy.LoadCA("")
	if err != nil {
		fmt.Fprintln(stderr, "rhythm echo:", err)
		return exitError
	}
	for _, n := range caNotes(ca) {
		if n.label == "Warning" {
			fmt.Fprintln(stderr, "rhythm echo: warning:", n.text)
		} else {
			fmt.Fprintln(stderr, "rhythm echo:", n.text)
		}
	}
	if *pem {
		_, _ = stdout.Write(ca.PEM)
		return exitOK
	}
	fmt.Fprintln(stdout, ca.CertPath)
	return exitOK
}

// systemTrustWarning is shown when the host itself trusts Echo's CA.
const systemTrustWarning = "this machine's system store trusts Echo's CA, so every program here accepts certificates Echo signs; trust it only inside containers (rhythm echo ca --untrust prints how to remove it)"

// caNote is one line about the CA for up's banner: CA for what LoadCA
// changed, Warning when the host's own trust store holds it.
type caNote struct{ label, text string }

func caNotes(ca *proxy.CA) []caNote {
	var notes []caNote
	for _, n := range ca.Notices {
		notes = append(notes, caNote{"CA", n})
	}
	if ca.TrustedBySystem() {
		notes = append(notes, caNote{"Warning", systemTrustWarning})
	}
	return notes
}

// untrustSteps lists commands that remove Echo's CA from the trust
// stores the docs add it to, with each certificate's fingerprints so the
// right one is removed, then where the CA itself lives.
func untrustSteps(certs []*x509.Certificate, dirs []string) string {
	var b strings.Builder
	if len(certs) == 0 {
		b.WriteString("No Echo CA on this machine. If one was trusted before, remove it with:\n")
	} else {
		b.WriteString("To stop trusting Echo's CA, run the lines for each place you added it:\n")
		for _, c := range certs {
			s1, s256 := sha1.Sum(c.Raw), sha256.Sum256(c.Raw) //nolint:gosec // SHA-1 is how macOS and Windows name certificates
			fmt.Fprintf(&b, "\n  %s, serial %X, expires %s\n    SHA-256 %X\n    SHA-1   %X\n", c.Subject.CommonName, c.SerialNumber, c.NotAfter.UTC().Format("2 Jan 2006"), s256, s1)
		}
	}
	b.WriteString(`
  Debian, Ubuntu, Alpine: sudo rm /usr/local/share/ca-certificates/rhythm-echo.crt && sudo update-ca-certificates
  Fedora, RHEL:           sudo rm /etc/pki/ca-trust/source/anchors/rhythm-echo.crt && sudo update-ca-trust
  macOS:                  sudo security delete-certificate -Z <SHA-1> /Library/Keychains/System.keychain
  Windows (admin shell):  certutil -delstore Root <SHA-1>
  Chrome, Firefox (NSS):  certutil -d sql:$HOME/.pki/nssdb -D -n "<the name you gave it>"
  Containers:             drop the line that copies ca.pem in and rebuild; unset NODE_EXTRA_CA_CERTS, REQUESTS_CA_BUNDLE, SSL_CERT_FILE or CURL_CA_BUNDLE where they point at it
`)
	if len(dirs) > 0 {
		b.WriteString("\nTo delete the CA itself, so nothing can sign with it again (Echo makes a new one when it next needs one):\n")
	}
	for _, d := range dirs {
		b.WriteString("  " + removeDir(d, runtime.GOOS) + "\n")
	}
	return b.String()
}

// removeDir is the command that deletes folder dir on goos: rm -r with a
// shell-quoted path, or cmd.exe's rmdir on Windows, which has no rm.
func removeDir(dir, goos string) string {
	if goos == "windows" {
		return `rmdir /s /q "` + dir + `"`
	}
	return "rm -r " + shellQuote(dir)
}

// cmdSendmail is a sendmail-compatible entry for PHP's sendmail_path and
// other tools. Its flags are sendmail's, so they bypass Echo's parser.
func cmdSendmail(args []string, stdin io.Reader, stderr io.Writer) int {
	return sendmail.Main(args, stdin, stderr)
}
