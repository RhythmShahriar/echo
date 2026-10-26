// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrintMap(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"-print-map", "-listen", "127.0.0.1:23400"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	line := strings.TrimSpace(out.String())
	for _, want := range []string{"--map-host '*.shop.test=127.0.0.1:23400'", "--map-host www.googletagmanager.com=127.0.0.1:23400", "--map-only"} {
		if !strings.Contains(line, want) {
			t.Errorf("-print-map lacks %q: %s", want, line)
		}
	}
}

func TestMakeCA(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ca")
	var out, errOut bytes.Buffer
	if code := run([]string{"-make-ca", dir}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "ca.pem")); err != nil {
		t.Error(err)
	}
	if code := run([]string{"-make-ca", dir}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "exists") {
		t.Errorf("second -make-ca: exit %d, %s", code, errOut.String())
	}
}

func TestBadRootAndFlags(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"-root", t.TempDir()}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "no sites folder") {
		t.Errorf("empty root: exit %d, %s", code, errOut.String())
	}
	if code := run([]string{"-nope"}, &out, &errOut); code != 2 {
		t.Errorf("unknown flag: exit %d", code)
	}
	errOut.Reset()
	if code := run([]string{"-tls", "-root", t.TempDir()}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "make ca creates it") {
		t.Errorf("-tls without a CA: exit %d, %s", code, errOut.String())
	}
}

// TestGateCerts: the gate's certificate covers every site, its
// sub-hosts and every stand-in host, chains to the testbed CA and
// nothing else; self-signed.shop.test gets a certificate the CA didn't
// sign.
func TestGateCerts(t *testing.T) {
	ca := filepath.Join(t.TempDir(), "ca")
	dir := filepath.Join(t.TempDir(), "gate")
	var out, errOut bytes.Buffer
	if code := run([]string{"-gate-certs", dir, "-root", filepath.Join("..", "..", ".."), "-ca", ca}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "make ca creates it") {
		t.Errorf("no CA: exit %d, %s", code, errOut.String())
	}
	if code := run([]string{"-make-ca", ca}, &out, &errOut); code != 0 {
		t.Fatal(errOut.String())
	}
	out.Reset()
	if code := run([]string{"-gate-certs", dir, "-root", filepath.Join("..", "..", ".."), "-ca", ca}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "wrote the gate's certificates to "+dir) {
		t.Errorf("output = %q", out.String())
	}
	pool := x509.NewCertPool()
	caPEM, _ := os.ReadFile(filepath.Join(ca, "ca.pem"))
	pool.AppendCertsFromPEM(caPEM)
	leaf := func(file string) *x509.Certificate {
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := pem.Decode(data)
		c, err := x509.ParseCertificate(b.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	gate := leaf("gate.pem")
	for _, name := range []string{"c01.shop.test", "c01-wp.shop.test", "metrics.c12.shop.test", "www.googletagmanager.com", "connect.facebook.net"} {
		if _, err := gate.Verify(x509.VerifyOptions{DNSName: name, Roots: pool}); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, name := range []string{"example.com", "a.b.c.shop.test"} {
		if _, err := gate.Verify(x509.VerifyOptions{DNSName: name, Roots: pool}); err == nil {
			t.Errorf("%s verifies", name)
		}
	}
	if _, err := leaf("self-signed.pem").Verify(x509.VerifyOptions{DNSName: "self-signed.shop.test", Roots: pool}); err == nil {
		t.Error("the self-signed certificate chains to the testbed CA")
	}
	if _, err := tls.LoadX509KeyPair(filepath.Join(dir, "gate.pem"), filepath.Join(dir, "gate-key.pem")); err != nil {
		t.Errorf("gate key pair: %v", err)
	}
}

// TestInternalFlag: -internal sets the address the ssrf-probe page asks
// for (in Docker the shop's service name), apart from where the admin
// listener binds.
func TestInternalFlag(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"-internal", "shop:8081", "-listen", "256.0.0.1:1", "-root", filepath.Join("..", "..", "..")}, &out, &errOut); code != 1 {
		t.Errorf("a bad -listen: exit %d", code)
	}
}
