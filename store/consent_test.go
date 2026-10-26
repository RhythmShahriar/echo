// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package store

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
	"time"
)

// Consent results get their own folder like runs, never shared even
// within one second; baselines are named folders that are never
// replaced by NewDir.
func TestConsentKinds(t *testing.T) {
	s := Store{Root: t.TempDir()}
	stamp := Stamp(time.Date(2026, 10, 26, 9, 20, 41, 0, time.UTC))
	a, err := s.NewDir(Consent, stamp)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.NewDir(Consent, stamp)
	if err != nil {
		t.Fatal(err)
	}
	pattern := regexp.MustCompile(`^2026-10-26T09-20-41-[0-9a-f]{6}$`)
	for _, dir := range []string{a, b} {
		if filepath.Dir(dir) != filepath.Join(s.Root, "consent") || !pattern.MatchString(filepath.Base(dir)) {
			t.Errorf("consent folder %s", dir)
		}
	}
	if a == b {
		t.Fatal("two consent results share a folder")
	}
	base, err := s.NewDir(ConsentBaselines, "baseline")
	if err != nil {
		t.Fatal(err)
	}
	if base != filepath.Join(s.Root, "consent", "baselines", "baseline") {
		t.Errorf("baseline folder %s", base)
	}
	if _, err := s.NewDir(ConsentBaselines, "baseline"); err == nil {
		t.Error("an existing baseline was replaced")
	}
	if _, err := s.NewDir(Consent, "../x"); err == nil {
		t.Error("a path name was taken")
	}
	if runtime.GOOS != "windows" {
		for _, dir := range []string{a, filepath.Dir(a), filepath.Dir(base)} {
			fi, err := os.Stat(dir)
			if err != nil {
				t.Fatal(err)
			}
			if fi.Mode().Perm() != dirMode {
				t.Errorf("%s is %v; want %v", dir, fi.Mode().Perm(), os.FileMode(dirMode))
			}
		}
	}
}
