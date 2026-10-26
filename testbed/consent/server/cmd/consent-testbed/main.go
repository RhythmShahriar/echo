// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Command consent-testbed serves the Echo consent testbed in Go mode:
// every scenario site by its host name (c01.shop.test ... g15.shop.test
// and the security fixtures), the stand-in tracker tags and CMP replicas
// on their real host names plus a sink that counts every tracker hit
// that reaches it. rhythm-echo reaches it through the --map-host flags
// that -print-map prints.
//
//	consent-testbed [-root testbed/consent] [-listen 127.0.0.1:23400] [-admin 127.0.0.1:23401] [-tls]
//	consent-testbed -print-map
//	consent-testbed -make-ca testbed/consent/ca
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"

	"github.com/RhythmShahriar/echo/testbed/consent/server"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("consent-testbed", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", defaultRoot(), "the testbed/consent folder (sites/, state/, ca/)")
	listen := fs.String("listen", server.DefaultListen, "address of the sites and stand-in tags")
	admin := fs.String("admin", server.DefaultAdmin, "address of the admin and sink endpoints")
	useTLS := fs.Bool("tls", false, "also serve https on -listen, with certificates from the testbed CA")
	caDir := fs.String("ca", "", "the testbed CA folder (default <root>/ca)")
	makeCA := fs.String("make-ca", "", "write a new testbed CA into this folder and exit")
	printMap := fs.Bool("print-map", false, "print the rhythm-echo --map-host flags for -listen and exit")
	echoProxy := fs.String("echo-proxy", "", "Echo's proxy (http://127.0.0.1:8026) for c18's server-side event; without it none is sent")
	echoCA := fs.String("echo-ca", "", "Echo's CA certificate (PEM) for -echo-proxy")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *printMap {
		fmt.Fprintln(stdout, server.MapLine(*listen))
		return 0
	}
	if *makeCA != "" {
		if err := server.MakeCA(*makeCA); err != nil {
			fmt.Fprintln(stderr, "consent-testbed:", err)
			return 1
		}
		fmt.Fprintln(stdout, "consent-testbed: wrote the testbed CA to", *makeCA)
		return 0
	}
	opts := server.Options{Root: *root, Admin: *admin, EchoProxy: *echoProxy, EchoCA: *echoCA,
		Log: func(m string) { fmt.Fprintln(stderr, "consent-testbed:", m) }}
	if *useTLS {
		dir := *caDir
		if dir == "" {
			dir = filepath.Join(*root, "ca")
		}
		ca, err := server.LoadCA(dir)
		if err != nil {
			fmt.Fprintln(stderr, "consent-testbed:", err)
			return 1
		}
		opts.CA = ca
	}
	srv, err := server.New(opts)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	sites, err := net.Listen("tcp", *listen)
	if err != nil {
		fmt.Fprintln(stderr, "consent-testbed:", err)
		return 1
	}
	adm, err := net.Listen("tcp", *admin)
	if err != nil {
		fmt.Fprintln(stderr, "consent-testbed:", err)
		return 1
	}
	scheme := "http"
	if *useTLS {
		scheme = "http and https"
	}
	fmt.Fprintf(stdout, "consent-testbed: sites and tags on %s (%s), admin and sink on %s\n", *listen, scheme, *admin)
	fmt.Fprintf(stdout, "consent-testbed: map with %s\n", server.MapLine(*listen))
	errc := make(chan error, 2)
	go func() { errc <- srv.ServeAdmin(adm) }()
	go func() { errc <- srv.Serve(sites) }()
	err = <-errc
	fmt.Fprintln(stderr, "consent-testbed:", err)
	return 1
}

// defaultRoot is testbed/consent when run from the repository root, or
// the current folder when run from inside testbed/consent.
func defaultRoot() string {
	for _, d := range []string{filepath.Join("testbed", "consent"), "."} {
		if fi, err := os.Stat(filepath.Join(d, "sites")); err == nil && fi.IsDir() {
			return d
		}
	}
	return filepath.Join("testbed", "consent")
}
