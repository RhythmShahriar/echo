// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cli

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent/consenttest"
)

// writeKeys writes a fresh ed25519 pair as openssl does: team.key
// (0600) and team.pub, in the working folder.
func writeKeys(t *testing.T) {
	t.Helper()
	pk, sk, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(sk)
	if err != nil {
		t.Fatal(err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(pk)
	if err != nil {
		t.Fatal(err)
	}
	putFile(t, "team.key", string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})))
	putFile(t, "team.pub", string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})))
}

// signedCheck runs a clean check signed with team.key and returns its
// result folder.
func signedCheck(t *testing.T) string {
	t.Helper()
	writeKeys(t)
	s := consenttest.Fakes()
	s.Runner = &filesRunner{consenttest.FakeRunner{Result: consenttest.GoldenResult("clean")}}
	consenttest.Use(t, s)
	fakeWriters(t)
	if code, _, errOut := runMain(t, "--sign-key", "team.key", "https://shop.test/"); code != exitOK {
		t.Fatalf("check: exit %d %s", code, errOut)
	}
	return resultDir(t)
}

// A signed check verifies with its public key: the signature is in the
// manifest, every file matches and the exit is 0.
func TestVerifySignedCheck(t *testing.T) {
	inDir(t, "")
	dir := signedCheck(t)
	if _, err := os.Stat(filepath.Join(dir, "consent.json.sig")); err != nil {
		t.Fatal("no consent.json.sig")
	}
	code, out, errOut := runMain(t, "verify", dir, "--key", "team.pub")
	want := "ok  consent.json\nok  consent.json.sig\nok  events/page-1-none.ndjson\nok  evidence/page-1/none-banner.png\nok  report.html\n" +
		"signature present=true ok=true key=team.pub\n"
	if code != exitOK || out != want || errOut != "" {
		t.Errorf("exit %d\n%s%s", code, out, errOut)
	}
	// Without --key the signature is reported as present: not checked, not failing.
	if code, out, _ := runMain(t, "verify", filepath.Join(dir, "consent.json")); code != exitOK || !strings.Contains(out, "signature present=true ok=false key=\n") {
		t.Errorf("no --key: exit %d\n%s", code, out)
	}
	// --json: the writer gets the folder relative to here.
	if code, out, _ := runMain(t, "verify", "--json", dir); code != exitOK || !strings.Contains(out, `"kind":"consent-verify"`) || !strings.Contains(out, `"folder":"`+filepath.ToSlash(dir)+`"`) {
		t.Errorf("--json: exit %d\n%s", code, out)
	}
}

// A flipped byte exits 1 naming the file; a signature checked with
// another key exits 1 too.
func TestVerifyTamperedExit(t *testing.T) {
	inDir(t, "")
	dir := signedCheck(t)
	path := filepath.Join(dir, "report.html")
	data, err := os.ReadFile(path) //nolint:gosec // the test's own folder
	if err != nil {
		t.Fatal(err)
	}
	data[0] ^= 1
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := runMain(t, "verify", dir, "--key", "team.pub"); code != exitFindings || !strings.Contains(out, "changed  report.html\n") {
		t.Errorf("tampered: exit %d\n%s", code, out)
	}
	data[0] ^= 1
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename("team.pub", "old.pub"); err != nil {
		t.Fatal(err)
	}
	writeKeys(t)
	if code, out, _ := runMain(t, "verify", dir, "--key", "team.pub"); code != exitFindings || !strings.Contains(out, "signature present=true ok=false") {
		t.Errorf("another key: exit %d\n%s", code, out)
	}
}

// A missing file exits 3; so does a folder that isn't a result. No
// folder, or two, is a usage error, as is a --key that isn't one.
func TestVerifyMissingExit(t *testing.T) {
	inDir(t, "")
	dir := signedCheck(t)
	if err := os.Remove(filepath.Join(dir, "evidence", "page-1", "none-banner.png")); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := runMain(t, "verify", dir, "--key", "team.pub"); code != exitUnavailable || !strings.Contains(out, "missing  evidence/page-1/none-banner.png\n") {
		t.Errorf("missing file: exit %d\n%s", code, out)
	}
	if err := os.MkdirAll("empty", 0o700); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := runMain(t, "verify", "empty"); code != exitUnavailable || errOut != "rhythm echo: consent verify empty: no manifest.json; is it a consent result folder?\n" {
		t.Errorf("no manifest: exit %d %q", code, errOut)
	}
	for _, args := range [][]string{{"verify"}, {"verify", "a", "b"}} {
		if code, _, errOut := runMain(t, args...); code != exitUsage || !strings.HasPrefix(errOut, "rhythm echo: consent verify needs one evidence folder") {
			t.Errorf("%v: exit %d %q", args, code, errOut)
		}
	}
	if code, _, errOut := runMain(t, "verify", dir, "--key", "team.key"); code != exitUsage || !strings.Contains(errOut, "want an ed25519 public key") {
		t.Errorf("private key as --key: exit %d %q", code, errOut)
	}
}

// The Action's keyless bundle is pointed at, not checked.
func TestVerifySigstoreLine(t *testing.T) {
	inDir(t, "")
	dir := signedCheck(t)
	putFile(t, filepath.Join(dir, "consent.json.sigstore.json"), "{}")
	code, out, _ := runMain(t, "verify", dir)
	if code != exitOK || !strings.HasSuffix(out, msgSigstore+"\n") || !strings.HasPrefix(msgSigstore, "Sigstore bundle present: check it with cosign verify-blob --bundle consent.json.sigstore.json --certificate-identity-regexp ") {
		t.Errorf("exit %d\n%s", code, out)
	}
	// With --json the line goes to stderr, keeping stdout one document.
	if _, out, errOut := runMain(t, "verify", "--json", dir); strings.Contains(out, "Sigstore") || !strings.Contains(errOut, "Sigstore bundle present") {
		t.Errorf("--json:\n%s\n%s", out, errOut)
	}
}
