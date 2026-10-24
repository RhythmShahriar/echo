package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/proxy"
	"github.com/RhythmShahriar/echo/sendmail"
)

// startProxy runs the HTTP(S) proxy for apps and SDKs that skip the
// connectors, when --proxy (or proxy: in the config) is set.
func startProxy(s *session, srv *catcher.Server) (func(), error) {
	cfg := s.cfg
	if cfg.Proxy == "" || cfg.Proxy == "off" {
		return func() {}, nil
	}
	var ca *proxy.CA
	if cfg.MITM {
		var err error
		if ca, err = proxy.LoadCA(""); err != nil {
			return nil, fmt.Errorf("loading the proxy CA: %w", err)
		}
	}
	p, err := proxy.New(proxy.Options{Server: srv, CA: ca, MITM: cfg.MITM, Allow: cfg.Allow, Token: s.echo.Token})
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", cfg.Proxy)
	if err != nil {
		return nil, fmt.Errorf("proxy on %s: %w", cfg.Proxy, err)
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
	s.view.Step("Proxy", line)
	return func() { _ = p.Close() }, nil
}

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
