// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Command catcher runs Echo's real catcher (capture API and SMTP sink) for
// the Laravel connector's end-to-end test, without the rhythm-echo CLI:
//
//	GOWORK=off go run ./connectors/laravel/fixtures/catcher -http 127.0.0.1:18960 -smtp 127.0.0.1:18961
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
)

func main() {
	httpAddr := flag.String("http", "127.0.0.1:18960", "capture API address")
	smtpAddr := flag.String("smtp", "127.0.0.1:18961", "SMTP sink address")
	mode := flag.String("mode", catcher.Seal, "seal or observe")
	allow := flag.String("allow", "", "comma-separated hosts allowed when sealed")
	token := flag.String("token", "", "required X-Echo-Token")
	flag.Parse()
	if err := run(*httpAddr, *smtpAddr, *mode, *allow, *token); err != nil {
		fmt.Fprintln(os.Stderr, "catcher:", err)
		os.Exit(1)
	}
}

func run(httpAddr, smtpAddr, mode, allow, token string) error {
	var hosts []string
	if allow != "" {
		hosts = strings.Split(allow, ",")
	}
	srv := catcher.NewServer(catcher.NewInbox(1000), catcher.Config{Mode: mode, Allow: hosts, Token: token, Version: "e2e"})

	ln, err := net.Listen("tcp", httpAddr)
	if err != nil {
		return err
	}
	api := &http.Server{Handler: srv, ReadHeaderTimeout: 10 * time.Second}
	sink := catcher.NewSMTP(srv, smtpAddr)

	errs := make(chan error, 2)
	go func() { errs <- api.Serve(ln) }()
	go func() { errs <- sink.ListenAndServe() }()
	fmt.Printf("catcher: api http://%s, smtp %s, mode %s\n", ln.Addr(), smtpAddr, srv.Mode())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case <-ctx.Done():
	case err := <-errs:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = sink.Close()
	return api.Shutdown(shut)
}
