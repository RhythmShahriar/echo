// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package scenarios

import (
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/testbed/consent/server"
)

// fixtureDirs are the folders under testbed/consent whose files a
// browser loads during a run: the scenario sites, the stand-in tags and
// CMP replicas the server serves and the WordPress mu-plugin that
// renders the Docker variants. A host named in them must be one the
// run maps to the testbed, or the page could reach the internet.
var fixtureDirs = []string{"sites", filepath.Join("server", "static"), filepath.Join("wp", "mu-plugins")}

// vendorHosts are the only hosts the vendored CMPs (Klaro and
// cookieconsent, served byte for byte as released and pinned by
// SHA-256) may name: links in their texts and comments, an error code
// URL and the SVG namespace, none of which the page fetches. A new
// release that names another host fails TestManifest so someone looks.
var vendorHosts = []string{"github.com", "fb.me", "www.w3.org", "kiprotect.com"}

var (
	// urlHost finds hosts in absolute and protocol-relative URLs. A host
	// needs a dot, so a regex literal like /x//g doesn't count.
	urlHost = regexp.MustCompile(`(?i)(?:\b[a-z][a-z0-9+.-]*:)?//(\[[0-9a-f:.]+\]|[a-z0-9*_-]+(?:\.[a-z0-9_-]+)+)`)
	// quotedHost finds a bare hostname in quotes, as a tag passes one
	// to a cookie domain or a loader builds a URL from parts. Only
	// names under a top-level domain a real site could use count, so
	// 'gtag.js' is a file and not a host.
	quotedHost = regexp.MustCompile(`(?i)["'` + "`" + `]((?:[a-z0-9-]+\.)+(?:com|net|org|io|eu|ms|de|co|uk|me|info|app|dev|ai|cloud|ly|gl|be|fr|it|nl|tv|xyz))["'` + "`" + `]`)
	email      = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]+`)
)

// secretShapes are the shapes of real credentials a fixture must never
// hold: fake tokens are made at run time in Go (fake-secrets rule), so
// push protection never sees one and no fixture can leak a real one.
// The prefixes are split so this file doesn't match them either.
var secretShapes = []*regexp.Regexp{
	regexp.MustCompile(`\b[sr]k_` + `(?:live|test)_[0-9A-Za-z]{16,}`),
	regexp.MustCompile(`\bA` + `KIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\bgh` + `[pousr]_[0-9A-Za-z]{36}\b`),
	regexp.MustCompile(`\bgithub_` + `pat_[0-9A-Za-z_]{40,}`),
	regexp.MustCompile(`\bxo` + `x[abprs]-[0-9A-Za-z-]{10,}`),
	regexp.MustCompile(`\bAI` + `za[0-9A-Za-z_-]{35}\b`),
	regexp.MustCompile(`\bS` + `G\.[0-9A-Za-z_-]{22}\.[0-9A-Za-z_-]{43}\b`),
	regexp.MustCompile(`\bwh` + `sec_[0-9A-Za-z+/=]{24,}`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIV` + `ATE KEY-----`),
	regexp.MustCompile(`\bey` + `J[0-9A-Za-z_-]{10,}\.ey` + `J[0-9A-Za-z_-]{10,}\.[0-9A-Za-z_-]{10,}`),
}

// auditManifest checks the manifest at root against what the benchmark
// relies on and returns one line per problem, each naming the scenario
// or the file: every scenario has a site folder, a toggle and an
// expect (bad) or an allow list (good); every check named is a consent
// check; every host in a fixture is mapped to the testbed; no fixture
// holds a real-looking secret.
func auditManifest(root string) []string {
	m, err := Load(root)
	if err != nil {
		return []string{err.Error()}
	}
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	ids := checkIDs()
	for _, s := range m.Scenarios {
		if _, err := os.Stat(filepath.Join(root, "sites", s.ID, "site.html")); err != nil {
			add("%s: no sites/%s/site.html", s.ID, s.ID)
		}
		if strings.TrimSpace(s.Toggle) == "" {
			add("%s: no toggle", s.ID)
		}
		if s.Good() && s.Allow == nil {
			add("%s: a good scenario needs an allow list", s.ID)
		}
		if !s.Good() && len(s.Expect) == 0 {
			add("%s: a bad scenario needs an expect", s.ID)
		}
		for _, e := range slices.Concat(s.Expect, s.Forbid) {
			if e.Check != "" && !slices.Contains(ids, e.Check) {
				add("%s: %q isn't a consent check", s.ID, e.Check)
			}
		}
		for _, a := range s.Allow {
			if !slices.Contains(ids, a) {
				add("%s: allow %q isn't a consent check", s.ID, a)
			}
		}
	}
	for _, f := range m.Fixtures {
		if _, err := os.Stat(filepath.Join(root, "sites", f.ID, "site.html")); err != nil {
			add("%s: no sites/%s/site.html", f.ID, f.ID)
		}
	}
	mapped := server.TagHosts()
	for _, dir := range fixtureDirs {
		problems = append(problems, auditFiles(root, dir, mapped)...)
	}
	return problems
}

// auditFiles scans every file under root/dir for hosts outside the
// testbed and for real-looking secrets. Markdown (SOURCE.md and the
// READMEs: where a replica's markup came from) and the license texts
// are documentation the server never sends, so their links stay.
func auditFiles(root, dir string, mapped []string) []string {
	var problems []string
	err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && d == nil {
				return fs.SkipDir
			}
			return err
		}
		if d.IsDir() || strings.HasSuffix(path, ".md") || d.Name() == "LICENSE" || d.Name() == "VERSIONS" {
			return nil
		}
		data, err := os.ReadFile(path) //nolint:gosec // the testbed's own fixtures
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		vendored := strings.Contains(rel, "/vendor/")
		for _, h := range hostsIn(string(data)) {
			if !allowedHost(h, mapped) && (!vendored || !slices.Contains(vendorHosts, h)) {
				problems = append(problems, fmt.Sprintf("%s: names %s, which isn't under .test, loopback or a host -print-map maps", rel, h))
			}
		}
		for _, re := range secretShapes {
			if loc := re.FindIndex(data); loc != nil {
				// Never the value: the line is enough to find it.
				line := 1 + strings.Count(string(data[:loc[0]]), "\n")
				problems = append(problems, fmt.Sprintf("%s:%d: holds something shaped like a real secret; make fake ones at run time in Go", rel, line))
			}
		}
		return nil
	})
	if err != nil {
		problems = append(problems, fmt.Sprintf("%s: %v", dir, err))
	}
	return problems
}

// hostsIn lists the hosts text names in URLs and quoted hostnames,
// lower case and once each. Email addresses (the file headers) aren't
// hosts a page loads.
func hostsIn(text string) []string {
	text = email.ReplaceAllString(text, "")
	var hosts []string
	for _, re := range []*regexp.Regexp{urlHost, quotedHost} {
		for _, m := range re.FindAllStringSubmatch(text, -1) {
			h := strings.ToLower(strings.Trim(m[1], "[]"))
			if !slices.Contains(hosts, h) {
				hosts = append(hosts, h)
			}
		}
	}
	slices.Sort(hosts)
	return hosts
}

// allowedHost reports whether a fixture may name h: the reserved .test
// and localhost names, a mapped stand-in host or an address that can't
// leave the machine or its network. The ssrf-probe site names private
// and link-local addresses on purpose: Echo must refuse them.
func allowedHost(h string, mapped []string) bool {
	if h == "localhost" || strings.HasSuffix(h, ".test") || strings.HasSuffix(h, ".localhost") || slices.Contains(mapped, h) {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
	}
	return false
}

// TestManifest runs the audit on the real testbed: it must find
// nothing.
func TestManifest(t *testing.T) {
	for _, p := range auditManifest(root) {
		t.Error(p)
	}
}

// TestManifestFindsProblems is the audit's negative side: a temporary
// testbed with a real host in a fixture, a secret-shaped literal, an
// unknown check and a missing site folder fails, each naming its file
// or scenario.
func TestManifestFindsProblems(t *testing.T) {
	dir := t.TempDir()
	manifest := "scenarios:\n" +
		"  - {id: c01, name: x, area: cmp, cmp: x, needs: go, toggle: x, expect: [{check: consent/nope}]}\n" +
		"  - {id: g01, name: x, area: cmp, cmp: x, needs: go, toggle: x, allow: [consent/also-nope]}\n" +
		"  - {id: g02, name: x, area: cmp, cmp: x, needs: go, toggle: x, allow: []}\n"
	key := "sk_" + "live_" + strings.Repeat("Tb0", 8)
	files := map[string]string{
		File:                               manifest,
		"sites/c01/site.html":              `<img src="https://www.example.com/pixel.gif">`,
		"sites/g01/site.html":              `<script src="https://www.googletagmanager.com/gtag/js?id=G-TESTBED001"></script>`,
		"server/static/tags/leak.js":       `fetch("//cdn.example.org/x.js"); document.cookie = "a=1; domain=" + 'tracker.example.net';`,
		"server/static/tags/key.js":        "var k = '" + key + "';",
		"server/static/cmps/x/SOURCE.md":   "Captured from https://www.example.com/docs",
		"server/static/cmps/vendor/v/v.js": `"https://github.com/x" "https://cdn.example.com/y"`,
		"wp/mu-plugins/testbed.php":        `<?php // http://127.0.0.1:23400 http://169.254.169.254 https://wordpress.org`,
	}
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := strings.Join(auditManifest(dir), "\n")
	for _, want := range []string{
		"sites/c01/site.html: names www.example.com",
		"server/static/tags/leak.js: names cdn.example.org",
		"server/static/tags/leak.js: names tracker.example.net",
		"server/static/tags/key.js:1: holds something shaped like a real secret",
		"server/static/cmps/vendor/v/v.js: names cdn.example.com",
		"wp/mu-plugins/testbed.php: names wordpress.org",
		`c01: "consent/nope" isn't a consent check`,
		`g01: allow "consent/also-nope" isn't a consent check`,
		"g02: no sites/g02/site.html",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the audit doesn't say %q; it said:\n%s", want, got)
		}
	}
	for _, never := range []string{key, "googletagmanager", "github.com", "SOURCE.md", "127.0.0.1", "169.254"} {
		if strings.Contains(got, never) {
			t.Errorf("the audit names %q:\n%s", never, got)
		}
	}
}

// TestHostsIn pins what counts as a host.
func TestHostsIn(t *testing.T) {
	text := `<a href="https://Shop.Example.com/x">` + ` //cdn.x.io/a.js /re//g 'gtag.js' "track.example.de"` +
		` // a comment, not.a.host ` + `me@rhy.io http://[::1]:8080/`
	want := []string{"::1", "cdn.x.io", "shop.example.com", "track.example.de"}
	if got := hostsIn(text); !slices.Equal(got, want) {
		t.Errorf("hostsIn = %v, want %v", got, want)
	}
	mapped := server.TagHosts()
	for h, ok := range map[string]bool{
		"c01.shop.test": true, "localhost": true, "127.0.0.1": true, "10.0.0.5": true, "169.254.169.254": true,
		"::1": true, "www.facebook.com": true, "www.example.com": false, "8.8.8.8": false, "facebook.com": false,
	} {
		if allowedHost(h, mapped) != ok {
			t.Errorf("allowedHost(%s) = %v, want %v", h, !ok, ok)
		}
	}
}
