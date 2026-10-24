// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetAddressesKeepsTheRest(t *testing.T) {
	p := write(t, Template)
	if err := SetAddresses(p, "127.0.0.1:18025", "127.0.0.1:11025", ""); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	want := strings.Replace(strings.Replace(Template,
		"listen: 127.0.0.1:8025  # API", "listen: 127.0.0.1:18025  # API", 1),
		"smtp: 127.0.0.1:1025    # SMTP", "smtp: 127.0.0.1:11025    # SMTP", 1)
	if string(got) != want {
		t.Errorf("only the two values may change:\n%s", diffLines(want, string(got)))
	}
	c, err := Load(p)
	if err != nil || c.Listen != "127.0.0.1:18025" || c.SMTP != "127.0.0.1:11025" || c.Proxy != "" {
		t.Errorf("reads back as %+v, %v", c, err)
	}
}

func TestSetAddressesShapes(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", "proxy: 127.0.0.1:8026\n"},
		{"# just a comment", "# just a comment\nproxy: 127.0.0.1:8026\n"},
		{"mode: seal\n", "mode: seal\nproxy: 127.0.0.1:8026\n"},
		{"proxy: \"\"   # off\nmode: seal\n", "proxy: 127.0.0.1:8026   # off\nmode: seal\n"},
		{"proxy:\nmode: seal\n", "proxy: 127.0.0.1:8026\nmode: seal\n"},
		{"proxy: # off\r\nmode: seal\r\n", "proxy: 127.0.0.1:8026 # off\r\nmode: seal\r\n"},
		{"'proxy': 'x#y' # c\n", "'proxy': 127.0.0.1:8026 # c\n"},
		{"proxy: a#b\n", "proxy: 127.0.0.1:8026\n"},
	} {
		p := write(t, tc.in)
		if err := SetAddresses(p, "", "", "127.0.0.1:8026"); err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		if got, _ := os.ReadFile(p); string(got) != tc.want {
			t.Errorf("%q:\n got %q\nwant %q", tc.in, got, tc.want)
		}
	}
}

func TestSetAddressesQuotesAndRefuses(t *testing.T) {
	p := write(t, "listen: 127.0.0.1:8025\n")
	if err := SetAddresses(p, "[::1]:8025", "", ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p); string(got) != "listen: \"[::1]:8025\"\n" {
		t.Errorf("got %q", got)
	}
	for _, in := range []string{"{listen: x}\n", "listen: |\n  x\n", "listen:\n  - x\n", "- a\n"} {
		p := write(t, in)
		if err := SetAddresses(p, "127.0.0.1:1", "", ""); err == nil {
			t.Errorf("%q: rewritten", in)
		}
		if got, _ := os.ReadFile(p); string(got) != in {
			t.Errorf("%q: changed on failure to %q", in, got)
		}
	}
	p = write(t, "modee: seal\n") // already broken: refuse rather than write
	if err := SetAddresses(p, "127.0.0.1:1", "", ""); err == nil || !strings.Contains(err.Error(), "modee") {
		t.Errorf("broken file: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Dir(p)); len(entries) != 1 {
		t.Errorf("temp files left: %v", entries)
	}
}

func TestSetAddressesCreatesAndKeepsMode(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := SetAddresses(p, "127.0.0.1:18025", "", ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p); string(got) != "listen: 127.0.0.1:18025\n" {
		t.Errorf("new file = %q", got)
	}
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetAddresses(p, "127.0.0.1:18026", "", ""); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", st.Mode().Perm())
	}
}

func diffLines(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	var out []string
	for i := range max(len(al), len(bl)) {
		var x, y string
		if i < len(al) {
			x = al[i]
		}
		if i < len(bl) {
			y = bl[i]
		}
		if x != y {
			out = append(out, "- "+x, "+ "+y)
		}
	}
	return strings.Join(out, "\n")
}
