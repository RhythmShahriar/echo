// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package evidence

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent/consenttest"
	"github.com/RhythmShahriar/echo/report"
	kitcli "github.com/RhythmShahriar/rhythm/kit/cli"
)

// testKeys writes a fresh ed25519 key pair as openssl writes them,
// team.key (0600) and team.pub; it returns their paths.
func testKeys(t *testing.T, dir string) (priv, pub string) {
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
	priv, pub = filepath.Join(dir, "team.key"), filepath.Join(dir, "team.pub")
	if err := os.WriteFile(priv, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pub, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return priv, pub
}

// folder writes a whole evidence folder as a check does: attachments,
// consent.json (signed with key when set), a report and the manifest.
func folder(t *testing.T, key string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "2026-10-26T09-20-41-c4d5e6")
	put(t, dir, "evidence/page-1/reject-banner.png", "png bytes")
	put(t, dir, "events/page-1-none.ndjson", "{\"kind\":\"request\"}\n")
	sha, err := WriteResult(dir, consenttest.GoldenResult("clean"), key)
	if err != nil {
		t.Fatal(err)
	}
	put(t, dir, "report.html", "<p>consent.json sha256 "+sha+"</p>")
	if err := WriteManifest(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

// failed lists the files verify didn't match.
func failed(v *report.ConsentVerify) []string {
	var out []string
	for _, f := range v.Files {
		if !f.OK {
			out = append(out, f.Path)
		}
	}
	return out
}

func isUsage(err error) bool {
	var ue kitcli.UsageError
	return errors.As(err, &ue)
}

// A signed folder verifies: every file, its hash in both lists and
// the signature with the matching public key.
func TestConsentVerifyGood(t *testing.T) {
	priv, pub := testKeys(t, t.TempDir())
	dir := folder(t, priv)
	v, err := Verify(dir, pub)
	if err != nil {
		t.Fatal(err)
	}
	if bad := failed(v); len(bad) != 0 {
		t.Fatalf("mismatched: %v", bad)
	}
	var paths []string
	for _, f := range v.Files {
		paths = append(paths, f.Path)
	}
	if strings.Join(paths, " ") != "consent.json consent.json.sig events/page-1-none.ndjson evidence/page-1/reject-banner.png report.html" {
		t.Errorf("files: %v", paths)
	}
	if !v.Signature.Present || !v.Signature.OK || v.Signature.Key != pub {
		t.Errorf("signature: %+v", v.Signature)
	}
	// The signature file names the key and holds a base64 ed25519 signature.
	data, err := os.ReadFile(filepath.Join(dir, SignatureFile)) //nolint:gosec // the test's own folder
	if err != nil {
		t.Fatal(err)
	}
	var s Signature
	if err := json.Unmarshal(data, &s); err != nil || s.Algorithm != "ed25519" || len(s.KeyID) != 16 || s.Signature == "" {
		t.Errorf("consent.json.sig: %s", data)
	}
	// Given the folder's consent.json instead of the folder: the same.
	if v2, err := Verify(filepath.Join(dir, ResultFile), pub); err != nil || len(failed(v2)) != 0 || !v2.Signature.OK {
		t.Errorf("by consent.json: %+v %v", v2, err)
	}
	// Without --key a signature is reported as present, not checked.
	v3, err := Verify(dir, "")
	if err != nil || !v3.Signature.Present || v3.Signature.OK || v3.Signature.Key != "" {
		t.Errorf("no key: %+v %v", v3.Signature, err)
	}
}

// One flipped byte in a report or in consent.json names that file; a
// changed consent.json also breaks the signature.
func TestConsentVerifyTampered(t *testing.T) {
	priv, pub := testKeys(t, t.TempDir())
	for _, name := range []string{"report.html", "consent.json", "evidence/page-1/reject-banner.png"} {
		t.Run(name, func(t *testing.T) {
			dir := folder(t, priv)
			path := filepath.Join(dir, filepath.FromSlash(name))
			data, err := os.ReadFile(path) //nolint:gosec // the test's own folder
			if err != nil {
				t.Fatal(err)
			}
			data[len(data)/2] ^= 1
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			v, err := Verify(dir, pub)
			if err != nil {
				t.Fatal(err)
			}
			bad := failed(v)
			if len(bad) == 0 || bad[0] != name {
				t.Fatalf("mismatched: %v", bad)
			}
			for _, f := range v.Files {
				if f.Path == name && (f.Got == "" || f.Got == f.Want) {
					t.Errorf("%s: %+v", name, f)
				}
			}
			if sigOK := name != "consent.json"; v.Signature.OK != sigOK {
				t.Errorf("signature ok %v, want %v", v.Signature.OK, sigOK)
			}
		})
	}
	// Another key's signature doesn't check.
	_, other := testKeys(t, t.TempDir())
	if v, err := Verify(folder(t, priv), other); err != nil || v.Signature.OK || !v.Signature.Present {
		t.Errorf("another key: %+v %v", v.Signature, err)
	}
	// With --key, a removed signature fails rather than passing unsigned.
	dir := folder(t, priv)
	if err := os.Remove(filepath.Join(dir, SignatureFile)); err != nil {
		t.Fatal(err)
	}
	if v, err := Verify(dir, pub); err != nil || v.Signature.OK || v.Signature.Present {
		t.Errorf("signature removed: %+v %v", v.Signature, err)
	}
}

// consent.json's own list of attachment hashes is checked too: a
// manifest rewritten to match a changed screenshot still fails.
func TestConsentVerifyRewrittenManifest(t *testing.T) {
	dir := folder(t, "")
	put(t, dir, "evidence/page-1/reject-banner.png", "another picture")
	if err := WriteManifest(dir); err != nil {
		t.Fatal(err)
	}
	v, err := Verify(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if bad := failed(v); len(bad) != 1 || bad[0] != "evidence/page-1/reject-banner.png" {
		t.Errorf("mismatched: %v", bad)
	}
	// One entry per file, the failed check winning, so the counts add
	// up to the files in the folder.
	seen := map[string]bool{}
	for _, f := range v.Files {
		if seen[f.Path] {
			t.Errorf("%s listed twice: %+v", f.Path, v.Files)
		}
		seen[f.Path] = true
	}
	if len(v.Files) != 4 {
		t.Errorf("%d entries: %+v", len(v.Files), v.Files)
	}
}

// A missing file has no hash; a folder without a manifest or with one
// naming files outside it can't be verified at all.
func TestVerifyMissingFile(t *testing.T) {
	dir := folder(t, "")
	if err := os.Remove(filepath.Join(dir, "report.html")); err != nil {
		t.Fatal(err)
	}
	v, err := Verify(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range v.Files {
		if f.Path == "report.html" && (f.OK || f.Got != "") {
			t.Errorf("missing file: %+v", f)
		}
	}
	var ue *UnreadableError
	if _, err := Verify(t.TempDir(), ""); !errors.As(err, &ue) || !strings.Contains(err.Error(), "no manifest.json; is it a consent result folder?") {
		t.Errorf("no manifest: %v", err)
	}
	if _, err := Verify(filepath.Join(t.TempDir(), "gone"), ""); !errors.As(err, &ue) {
		t.Errorf("no folder: %v", err)
	}
	for _, p := range []string{"../outside", "/etc/passwd", `evidence\x.png`, "a/../../b", "./consent.json"} {
		bad := t.TempDir()
		put(t, bad, "manifest.json", `{"schema":"rhythm-echo-consent-manifest/v1","files":[{"path":`+jsonString(p)+`,"bytes":1,"sha256":"00"}]}`)
		if _, err := Verify(bad, ""); !errors.As(err, &ue) || !strings.Contains(err.Error(), "outside the folder") {
			t.Errorf("%s: %v", p, err)
		}
	}
	notManifest := t.TempDir()
	put(t, notManifest, "manifest.json", `{"schema":"something-else"}`)
	if _, err := Verify(notManifest, ""); !errors.As(err, &ue) {
		t.Errorf("another schema: %v", err)
	}
}

// A folder linked out of the evidence folder (events -> elsewhere) is
// never read through, even when the file there has the listed hash; a
// plain folder in the same place is.
func TestVerifyLinkedFolder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	dir := folder(t, "")
	outside := t.TempDir()
	put(t, outside, "page-1-none.ndjson", "{\"kind\":\"request\"}\n")
	if err := os.RemoveAll(filepath.Join(dir, "events")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "events")); err != nil {
		t.Fatal(err)
	}
	v, err := Verify(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range v.Files {
		if f.Path == "events/page-1-none.ndjson" && (f.OK || f.Got != "") {
			t.Errorf("read through the link: %+v", f)
		}
	}
	// The same file in a real folder verifies.
	if err := os.Remove(filepath.Join(dir, "events")); err != nil {
		t.Fatal(err)
	}
	put(t, dir, "events/page-1-none.ndjson", "{\"kind\":\"request\"}\n")
	if v, err := Verify(dir, ""); err != nil || len(failed(v)) != 0 {
		t.Errorf("real folder: %v %v", failed(v), err)
	}
}

// Unlisted names files neither list covers, links included; none
// for a folder as a check wrote it.
func TestUnlisted(t *testing.T) {
	dir := folder(t, "")
	v, err := Verify(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := Unlisted(dir, v); len(got) != 0 {
		t.Errorf("clean folder: %v", got)
	}
	put(t, dir, "report.md", "added later")
	put(t, dir, "evidence/page-1/extra.png", "x")
	want := "evidence/page-1/extra.png report.md"
	if runtime.GOOS != "windows" {
		if err := os.Symlink(t.TempDir(), filepath.Join(dir, "zlink")); err != nil {
			t.Fatal(err)
		}
		want += " zlink"
	}
	if got := strings.Join(Unlisted(filepath.Join(dir, ResultFile), v), " "); got != want {
		t.Errorf("unlisted: %q, want %q", got, want)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// A key others can read is refused before anything is written.
func TestSignKeyMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix mode bits")
	}
	priv, _ := testKeys(t, t.TempDir())
	if err := os.Chmod(priv, 0o644); err != nil { //nolint:gosec // the point of the test
		t.Fatal(err)
	}
	dir := t.TempDir()
	_, err := WriteResult(dir, consenttest.GoldenResult("clean"), priv)
	if !isUsage(err) || err.Error() != "--sign-key "+priv+" is readable by others; chmod 600 it" {
		t.Fatalf("0644 key: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ResultFile)); err == nil {
		t.Error("consent.json was written unsigned")
	}
	if err := os.Chmod(priv, 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSignKey(priv); err != nil {
		t.Errorf("0400 key: %v", err)
	}
}

// Only a PKCS#8 ed25519 private key signs; a public key does only for
// --key.
func TestWrongKeyType(t *testing.T) {
	dir := t.TempDir()
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecDER, err := x509.MarshalPKCS8PrivateKey(ec)
	if err != nil {
		t.Fatal(err)
	}
	priv, pub := testKeys(t, dir)
	pubData, err := os.ReadFile(pub) //nolint:gosec // the test's own key
	if err != nil {
		t.Fatal(err)
	}
	const want = "--sign-key: want an ed25519 private key in PEM (openssl genpkey -algorithm ed25519 -out team.key)"
	for name, data := range map[string][]byte{
		"ecdsa":      pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: ecDER}),
		"public key": pubData,
		"not pem":    []byte("not a key"),
		"bad der":    pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte{1, 2, 3}}),
	} {
		path := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".key")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadSignKey(path); !isUsage(err) || err.Error() != want {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := LoadSignKey(filepath.Join(dir, "missing.key")); !isUsage(err) || !strings.HasPrefix(err.Error(), "--sign-key ") {
		t.Errorf("missing key: %v", err)
	}
	// --key wants the public half.
	const wantPub = "--key: want an ed25519 public key in PEM (openssl pkey -in team.key -pubout -out team.pub)"
	if _, err := LoadPublicKey(priv); !isUsage(err) || err.Error() != wantPub {
		t.Errorf("private key as --key: %v", err)
	}
	if _, err := Verify(folder(t, ""), filepath.Join(dir, "missing.pub")); !isUsage(err) {
		t.Errorf("missing --key: %v", err)
	}
}

// The folder is 0700 and every file in it 0600, the signature too.
func TestFileModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix mode bits")
	}
	priv, _ := testKeys(t, t.TempDir())
	dir := filepath.Join(t.TempDir(), "new", "result")
	if _, err := WriteResult(dir, consenttest.GoldenResult("clean"), priv); err != nil {
		t.Fatal(err)
	}
	if err := WriteManifest(dir); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("folder: %v %v", fi.Mode(), err)
	}
	for _, name := range []string{ResultFile, SignatureFile, ManifestFile} {
		if fi, err := os.Stat(filepath.Join(dir, name)); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v %v", name, fi.Mode(), err)
		}
	}
}

// Every attachment's hash in consent.json and every file's in
// manifest.json is the file's real one.
func TestConsentReportHashes(t *testing.T) {
	dir := folder(t, "")
	data, err := os.ReadFile(filepath.Join(dir, ResultFile)) //nolint:gosec // the test's own folder
	if err != nil {
		t.Fatal(err)
	}
	var r struct {
		Evidence struct {
			Files []struct{ Path, SHA256 string }
		}
	}
	if err := json.Unmarshal(data, &r); err != nil || len(r.Evidence.Files) != 2 {
		t.Fatalf("consent.json evidence: %+v %v", r.Evidence, err)
	}
	for _, f := range r.Evidence.Files {
		h, err := HashFile(filepath.Join(dir, filepath.FromSlash(f.Path)))
		if err != nil || h.SHA256 != f.SHA256 {
			t.Errorf("consent.json %s: %s, the file %s", f.Path, f.SHA256, h.SHA256)
		}
	}
	mdata, err := os.ReadFile(filepath.Join(dir, ManifestFile)) //nolint:gosec // the test's own folder
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err := json.Unmarshal(mdata, &m); err != nil || len(m.Files) != 4 {
		t.Fatalf("manifest: %s %v", mdata, err)
	}
	for _, f := range m.Files {
		h, err := HashFile(filepath.Join(dir, filepath.FromSlash(f.Path)))
		if err != nil || h.SHA256 != f.SHA256 || h.Bytes != f.Bytes {
			t.Errorf("manifest %s: %+v, the file %+v", f.Path, f, h)
		}
	}
}

// KeyID is what openssl's DER form hashes to, cut to 16 hex digits.
func TestKeyID(t *testing.T) {
	pub := ed25519.PublicKey(make([]byte, ed25519.PublicKeySize))
	if id := KeyID(pub); len(id) != 16 || id != KeyID(pub) {
		t.Errorf("KeyID: %q", id)
	}
	other := ed25519.PublicKey(append(make([]byte, ed25519.PublicKeySize-1), 1))
	if KeyID(pub) == KeyID(other) {
		t.Error("two keys share an id")
	}
}

func TestSafePath(t *testing.T) {
	for p, want := range map[string]bool{
		"consent.json": true, "evidence/page-1/a.png": true, "": false, ".": false, "..": false,
		"../x": false, "/x": false, "a//b": false, "a/./b": false, `a\b`: false, "a\x00b": false,
	} {
		if got := safePath(p); got != want {
			t.Errorf("safePath(%q) = %v", p, got)
		}
	}
}

// FuzzVerifyManifest: verify never panics on a hostile manifest and
// signature and never reads outside the folder.
func FuzzVerifyManifest(f *testing.F) {
	f.Add([]byte(`{"schema":"rhythm-echo-consent-manifest/v1","files":[{"path":"consent.json","bytes":2,"sha256":"x"}]}`), []byte(`{"algorithm":"ed25519","signature":"AA=="}`))
	f.Add([]byte(`{"files":[{"path":"../../etc/passwd"}]}`), []byte(`not json`))
	f.Add([]byte(`[]`), []byte(``))
	f.Fuzz(func(t *testing.T, manifest, sig []byte) {
		dir := t.TempDir()
		put(t, dir, "consent.json", `{"evidence":{"files":[{"path":"../x","sha256":"y"}]}}`)
		put(t, dir, ManifestFile, string(manifest))
		put(t, dir, SignatureFile, string(sig))
		v, err := Verify(dir, "")
		if err == nil {
			for _, file := range v.Files {
				if !safePath(file.Path) {
					t.Fatalf("checked %q", file.Path)
				}
			}
		}
	})
}
