// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package server

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"path"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// maxReplicaAge is how old a replica's capture may be before the test
// fails (qa.md 5.3): CMP markup drifts, so refreshing it is a scheduled
// job, never a surprise.
const maxReplicaAge = 180 * 24 * time.Hour

var captured = regexp.MustCompile(`(?m)^captured: (\d{4}-\d{2}-\d{2})$`)

// replicaAges reads the capture date of every cmps/<name>/SOURCE.md.
func replicaAges(t *testing.T, fsys fs.FS) map[string]time.Time {
	t.Helper()
	dirs, err := fs.ReadDir(fsys, "cmps")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]time.Time{}
	for _, d := range dirs {
		if !d.IsDir() || d.Name() == "vendor" {
			continue
		}
		src, err := fs.ReadFile(fsys, path.Join("cmps", d.Name(), "SOURCE.md"))
		if err != nil {
			t.Errorf("replica %s has no SOURCE.md", d.Name())
			continue
		}
		m := captured.FindSubmatch(src)
		if m == nil {
			t.Errorf("cmps/%s/SOURCE.md has no 'captured: YYYY-MM-DD' line", d.Name())
			continue
		}
		when, err := time.Parse(time.DateOnly, string(m[1]))
		if err != nil {
			t.Errorf("cmps/%s/SOURCE.md: %v", d.Name(), err)
			continue
		}
		// generic is hand-made: it has no vendor page to cite.
		if d.Name() != "generic" && !strings.Contains(string(src), "https://") {
			t.Errorf("cmps/%s/SOURCE.md names no source URL", d.Name())
		}
		out[d.Name()] = when
	}
	return out
}

func TestReplicaAge(t *testing.T) {
	ages := replicaAges(t, Static())
	for _, want := range []string{"cookiebot", "onetrust", "usercentrics", "didomi", "iubenda", "borlabs", "complianz", "cookieyes", "generic"} {
		if _, ok := ages[want]; !ok {
			t.Errorf("no replica %s", want)
		}
	}
	for name, when := range ages {
		if age := time.Since(when); age > maxReplicaAge {
			t.Errorf("the %s replica was captured on %s, %d days ago: refresh it from the vendor's current markup and update SOURCE.md", name, when.Format(time.DateOnly), int(age.Hours()/24))
		}
		if when.After(time.Now().Add(24 * time.Hour)) {
			t.Errorf("the %s replica's capture date %s is in the future", name, when.Format(time.DateOnly))
		}
	}
}

func TestReplicaAgeFailsWhenOld(t *testing.T) {
	old := time.Now().Add(-maxReplicaAge - 48*time.Hour).Format(time.DateOnly)
	fsys := mapFS(map[string]string{"cmps/acme/SOURCE.md": "captured: " + old + "\nhttps://acme.test/docs\n"})
	ages := replicaAges(t, fsys)
	if time.Since(ages["acme"]) <= maxReplicaAge {
		t.Errorf("%s isn't older than the limit", old)
	}
}

// TestVendoredHashes: the open-source CMPs are used as released, so each
// file must match the SHA-256 in cmps/vendor/VERSIONS; every file in
// the folder must be listed.
func TestVendoredHashes(t *testing.T) {
	fsys := Static()
	f, err := fsys.Open("cmps/vendor/VERSIONS")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	listed := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 4 {
			t.Errorf("VERSIONS line %q: want name version file sha256", line)
			continue
		}
		data, err := fs.ReadFile(fsys, path.Join("cmps/vendor", parts[2]))
		if err != nil {
			t.Errorf("%s: %v", parts[2], err)
			continue
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != parts[3] {
			t.Errorf("%s %s: %s has SHA-256 %s, VERSIONS says %s", parts[0], parts[1], parts[2], got, parts[3])
		}
		listed[parts[2]] = true
	}
	for _, name := range []string{"klaro/LICENSE", "cookieconsent/LICENSE"} {
		if !listed[name] {
			t.Errorf("%s isn't listed: every vendored CMP ships its license", name)
		}
	}
	err = fs.WalkDir(fsys, "cmps/vendor", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Base(p) == "VERSIONS" {
			return err
		}
		if rel := strings.TrimPrefix(p, "cmps/vendor/"); !listed[rel] {
			t.Errorf("%s is vendored but not in VERSIONS", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func mapFS(files map[string]string) fstest.MapFS {
	m := fstest.MapFS{}
	for k, v := range files {
		m[k] = &fstest.MapFile{Data: []byte(v)}
	}
	return m
}
