// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package kitcheck

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/RhythmShahriar/rhythm/kit/update"
)

// release is an in-memory release of rhythm-echo for linux/amd64.
type release struct{ assets map[string][]byte }

const archiveName = "rhythm-echo_linux_amd64.tar.gz"

func (r release) Latest(context.Context) (*update.Release, error) { return r.rel(), nil }

func (r release) ByTag(context.Context, string) (*update.Release, error) { return r.rel(), nil }

func (r release) Open(_ context.Context, _ *update.Release, asset string) (io.ReadCloser, int64, error) {
	b, ok := r.assets[asset]
	if !ok {
		return nil, 0, fmt.Errorf("no asset %s", asset)
	}
	return io.NopCloser(bytes.NewReader(b)), int64(len(b)), nil
}

func (r release) rel() *update.Release {
	rel := &update.Release{Version: "1.2.0", Tag: "v1.2.0", Assets: map[string]string{}}
	for name := range r.assets {
		rel.Assets[name] = "mem://" + name
	}
	return rel
}

// signer stands in for kit/sigverify: the "bundle" is the sha256 of the
// checksums.txt it signed, so any other checksums.txt fails.
type signer struct{}

func (signer) VerifyChecksums(_ context.Context, _, _ string, checksums, bundle []byte) (string, error) {
	if fmt.Sprintf("%x", sha256.Sum256(checksums)) != string(bundle) {
		return "", errors.New("bundle does not cover this checksums.txt")
	}
	return "https://github.com/RhythmShahriar/.github/.github/workflows/release-go.yml@refs/heads/master", nil
}

func archive(t *testing.T, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&tar.Header{Name: "rhythm-echo", Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sums(a []byte) []byte { return fmt.Appendf(nil, "%x  %s\n", sha256.Sum256(a), archiveName) }

// signed is a release whose checksums.txt the bundle covers.
func signed(t *testing.T) release {
	a := archive(t, "#!/bin/sh\necho rhythm-echo 1.2.0\n")
	s := sums(a)
	return release{assets: map[string][]byte{archiveName: a, "checksums.txt": s,
		"checksums.txt.sigstore.json": fmt.Appendf(nil, "%x", sha256.Sum256(s))}}
}

func install(t *testing.T, r release) (string, error) {
	t.Helper()
	dest := filepath.Join(t.TempDir(), "rhythm-echo")
	if err := os.WriteFile(dest, []byte("old"), 0o700); err != nil { //nolint:gosec // a stand-in binary
		t.Fatal(err)
	}
	u := &update.Updater{Repo: "RhythmShahriar/echo", Binary: "rhythm-echo", Current: "1.1.0", OS: "linux", Arch: "amd64",
		Source: r, Verifier: signer{}}
	rel, err := u.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = u.Install(context.Background(), rel, dest)
	b, rerr := os.ReadFile(dest) //nolint:gosec // the test's own file
	if rerr != nil {
		t.Fatal(rerr)
	}
	return string(b), err
}

// TestUpdateInstallsSigned is the control: a release whose bundle covers
// its checksums.txt installs.
func TestUpdateInstallsSigned(t *testing.T) {
	got, err := install(t, signed(t))
	if err != nil || got != "#!/bin/sh\necho rhythm-echo 1.2.0\n" {
		t.Fatalf("install: %v, binary %q", err, got)
	}
}

// TestUpdateRefusesTamperedChecksums: someone swapped the archive and
// rewrote checksums.txt to match it. They can't sign it, so nothing changes.
func TestUpdateRefusesTamperedChecksums(t *testing.T) {
	r := signed(t)
	evil := archive(t, "#!/bin/sh\necho owned\n")
	r.assets[archiveName], r.assets["checksums.txt"] = evil, sums(evil)
	got, err := install(t, r)
	var sig *update.ErrSignature
	if !errors.As(err, &sig) {
		t.Fatalf("err = %v, want ErrSignature", err)
	}
	if got != "old" {
		t.Errorf("the binary was replaced: %q", got)
	}
}

// TestUpdateRefusesMissingBundle: a release without its signature fails
// closed rather than trusting checksums.txt alone.
func TestUpdateRefusesMissingBundle(t *testing.T) {
	r := signed(t)
	delete(r.assets, "checksums.txt.sigstore.json")
	got, err := install(t, r)
	var sig *update.ErrSignature
	if !errors.As(err, &sig) {
		t.Fatalf("err = %v, want ErrSignature", err)
	}
	if got != "old" {
		t.Errorf("the binary was replaced: %q", got)
	}
}

// TestUpdateRefusesWrongArchive: a signed checksums.txt that doesn't
// match the archive served is a checksum error, also with nothing changed.
func TestUpdateRefusesWrongArchive(t *testing.T) {
	r := signed(t)
	r.assets[archiveName] = archive(t, "#!/bin/sh\necho other\n")
	got, err := install(t, r)
	var sum *update.ErrChecksum
	if !errors.As(err, &sum) {
		t.Fatalf("err = %v, want ErrChecksum", err)
	}
	if got != "old" {
		t.Errorf("the binary was replaced: %q", got)
	}
}
