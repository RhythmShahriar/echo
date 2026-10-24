// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strings"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/proxy"
	"github.com/RhythmShahriar/echo/sendmail"
)

// startProxy serves the HTTP(S) proxy on ln, for apps and SDKs that skip
// the connectors (--proxy, or proxy: in the config). up binds ln with its
// other listeners, so nothing is printed before every port is taken. It
// returns the line up prints for the proxy and the proxy's shutdown.
func startProxy(s *session, srv *catcher.Server, ln net.Listener) (string, func(context.Context), error) {
	cfg := s.cfg
	var ca *proxy.CA
	if cfg.MITM {
		var err error
		if ca, err = proxy.LoadCA(""); err != nil {
			return "", nil, fmt.Errorf("loading the proxy CA: %w", err)
		}
	}
	p, err := proxy.New(proxy.Options{Server: srv, CA: ca, MITM: cfg.MITM, Allow: cfg.Allow, Token: s.echo.Token,
		Log: slog.New(viewHandler{log: s.view.Log})})
	if err != nil {
		return "", nil, err
	}
	go func() {
		if err := p.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.view.Log("proxy: " + err.Error())
		}
	}()
	line := cfg.Proxy + "  (HTTPS_PROXY for SDKs that skip the connector)"
	if cfg.MITM {
		line += "; decrypting HTTPS, trust " + ca.CertPath
	}
	return line, stopProxy(p), nil
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
func cmdCA(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ca", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	pem := fs.Bool("pem", false, "print the certificate instead of its path")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stdout, "usage: rhythm echo ca [--pem]   (--pem prints the certificate instead of its path)")
			return exitOK
		}
		fmt.Fprintln(stderr, "rhythm echo: usage: rhythm echo ca [--pem]")
		return exitUsage
	}
	ca, err := proxy.LoadCA("")
	if err != nil {
		fmt.Fprintln(stderr, "rhythm echo:", err)
		return exitError
	}
	if *pem {
		_, _ = stdout.Write(ca.PEM)
		return exitOK
	}
	fmt.Fprintln(stdout, ca.CertPath)
	return exitOK
}

// cmdSendmail is a sendmail-compatible entry for PHP's sendmail_path and
// other tools. Its flags are sendmail's, so they bypass Echo's parser.
func cmdSendmail(args []string, stdin io.Reader, stderr io.Writer) int {
	return sendmail.Main(args, stdin, stderr)
}
