// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
)

// put writes a file under dir, making its folders 0700.
func put(t *testing.T, dir, rel, data string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func sum(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

// WriteResult lists every attachment by hash in consent.json and
// returns consent.json's own hash; other files aren't attachments.
func TestWriteResult(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "evidence/page-1/reject-banner.png", "png")
	put(t, dir, "events/page-1-none.ndjson", "{}\n")
	put(t, dir, "har/page-1-reject.har", "{\"log\":{}}")
	put(t, dir, "report.html", "not an attachment")
	put(t, dir, "events/.tmp-123", "half written")
	r := consenttest.GoldenResult("clean")
	r.Evidence.Folder = ""
	sha, err := WriteResult(dir, r, "")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "consent.json")) //nolint:gosec // the test's own folder
	if err != nil {
		t.Fatal(err)
	}
	if sha != sum(data) {
		t.Errorf("returned %s; the file hashes to %s", sha, sum(data))
	}
	var got consent.Result
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range got.Evidence.Files {
		paths = append(paths, f.Path)
	}
	if strings.Join(paths, " ") != "events/page-1-none.ndjson evidence/page-1/reject-banner.png har/page-1-reject.har" {
		t.Errorf("attachments: %v", paths)
	}
	if f := got.Evidence.Files[1]; f.Bytes != 3 || f.SHA256 != sum([]byte("png")) {
		t.Errorf("the png: %+v", f)
	}
	if got.Evidence.Folder != filepath.ToSlash(dir) || got.ID != r.ID || len(got.Pages) != len(r.Pages) {
		t.Errorf("consent.json: folder %q id %q", got.Evidence.Folder, got.ID)
	}
	// A folder the caller names is kept.
	r.Evidence.Folder = ".echo/consent/x"
	if _, err := WriteResult(dir, r, ""); err != nil || r.Evidence.Folder != ".echo/consent/x" {
		t.Errorf("folder %q %v", r.Evidence.Folder, err)
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(filepath.Join(dir, "consent.json")); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("consent.json mode %v %v", fi.Mode().Perm(), err)
		}
	}
	// No attachments is an empty list, never null.
	empty := t.TempDir()
	if _, err := WriteResult(empty, consenttest.GoldenResult("clean"), ""); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(empty, "consent.json")); !strings.Contains(string(data), `"files": []`) { //nolint:gosec // the test's own folder
		t.Error("no attachments isn't an empty list")
	}
}

// The manifest covers every file but itself, sorted, links left out.
func TestWriteManifest(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "report.html", "<html>")
	put(t, dir, "consent.json", "{}")
	put(t, dir, "evidence/page-1/none-banner.png", "png")
	put(t, dir, "manifest.json", "old")
	if runtime.GOOS != "windows" {
		if err := os.Symlink("/etc/hostname", filepath.Join(dir, "link")); err != nil {
			t.Fatal(err)
		}
	}
	if err := WriteManifest(dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json")) //nolint:gosec // the test's own folder
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range m.Files {
		paths = append(paths, f.Path)
	}
	if m.Schema != ManifestSchema || strings.Join(paths, " ") != "consent.json evidence/page-1/none-banner.png report.html" {
		t.Errorf("manifest: %s", data)
	}
	if m.Files[2].SHA256 != sum([]byte("<html>")) || m.Files[2].Bytes != 6 {
		t.Errorf("report.html: %+v", m.Files[2])
	}
	if err := WriteManifest(filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing folder wrote a manifest")
	}
}
