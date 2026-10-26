// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
	kitcli "github.com/RhythmShahriar/rhythm/kit/cli"
)

// inDir runs the rest of t in a new empty folder, with a config of cfg
// when it isn't ""; it returns the folder.
func inDir(t *testing.T, cfg string) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	if cfg != "" {
		putFile(t, filepath.Join(".echo", "config.yaml"), cfg)
	}
	return dir
}

// putFile writes a test file, making its folder.
func putFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

// prepareArgs builds the setup for a check's command line.
func prepareArgs(t *testing.T, args ...string) (*setup, error) {
	t.Helper()
	o, pos, err := parse(subCheck, args)
	if err != nil {
		return nil, err
	}
	var errOut bytes.Buffer
	c := &cmd{env: Env{Version: "1.2.0", Stdout: &bytes.Buffer{}, Stderr: &errOut}, o: o}
	return c.prepare(pos)
}

// mustPrepare is prepareArgs that fails t on an error.
func mustPrepare(t *testing.T, args ...string) *setup {
	t.Helper()
	st, err := prepareArgs(t, args...)
	if err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	return st
}

// The defaults of PRD 7 when nothing is set.
func TestPlanDefaults(t *testing.T) {
	inDir(t, "")
	st := mustPrepare(t, "https://shop.test/")
	p := st.plans[0]
	if len(st.plans) != 1 || len(p.Targets) != 1 || p.Targets[0].URL.String() != "https://shop.test/" ||
		strings.Join(runNames(p.Runs), ",") != "none,accept,reject" || p.Pages != 1 || p.Settle != 3*time.Second ||
		p.BannerWait != 10*time.Second || p.Timeout != 90*time.Second || p.Retries != 1 || p.Parallel != 3 || p.CMP != "auto" ||
		p.Click != consent.ClickButton || p.Policy != consent.PolicyAdvanced || !p.Hold || p.Viewport != "desktop" ||
		p.Locale != "" || p.Timezone != "" || !p.Screenshots || p.HAR || p.ShowIDs || p.Echo != nil || p.Version != "1.2.0" ||
		p.Limits != consent.DefaultLimits {
		t.Errorf("defaults: %+v", p)
	}
	if st.store.Root != ".echo" || st.failOn != "error" {
		t.Errorf("store %q, fail-on %q", st.store.Root, st.failOn)
	}
}

func runNames(runs []consent.RunKind) []string {
	out := make([]string, len(runs))
	for i, r := range runs {
		out[i] = string(r)
	}
	return out
}

// TestPrecedence: the defaults, then the config, then the flags, then
// a site's own args for its pages.
func TestPrecedence(t *testing.T) {
	inDir(t, `site: https://staging.shop.test
consent:
  urls: [/, /shop/]
  pages: 2
  settle: 5s
  retries: 2
  geo: fr
  cmp: cookiebot
  exempt: [plausible]
checks:
  consent/cookieless-ping: warning
  consent/embed: off
`)
	st := mustPrepare(t)
	p := st.plans[0]
	if len(p.Targets) != 2 || p.Targets[1].URL.String() != "https://staging.shop.test/shop/" || p.Pages != 2 || p.Settle != 5*time.Second ||
		p.Retries != 2 || p.Geo != "fr" || p.CMP != "cookiebot" || p.Timeout != 90*time.Second || len(p.Exempt) != 1 {
		t.Errorf("config over the defaults: %+v", p)
	}
	if p.CheckLevels["consent/cookieless-ping"] != "warning" || p.CheckLevels["consent/embed"] != "off" || len(p.CheckLevels) != 2 {
		t.Errorf("check levels: %v", p.CheckLevels)
	}
	if !strings.HasSuffix(st.store.Root, ".echo") || !filepath.IsAbs(st.store.Root) {
		t.Errorf("the store is %q; want the config's folder", st.store.Root)
	}
	p = mustPrepare(t, "--pages", "0", "--geo", "DE", "--exempt", "fathom", "https://other.test/").plans[0]
	if p.Pages != 0 || p.Geo != "de" || p.Settle != 5*time.Second || len(p.Targets) != 1 || p.Targets[0].URL.Host != "other.test" ||
		strings.Join(p.Exempt, ",") != "plausible,fathom" {
		t.Errorf("flags over the config: %+v", p)
	}
	putFile(t, "sites.yaml", `sites:
  - name: brew
    url: https://brew.example
    consent:
      urls: [/, /menu/]
      args: ["--geo", "at", "--cmp", "complianz", "--pages=3"]
  - name: shop
    url: shop.example
`)
	st = mustPrepare(t, "--sites", "sites.yaml", "--pages", "1", "--geo", "de")
	if len(st.plans) != 2 {
		t.Fatalf("plans: %d", len(st.plans))
	}
	brew, shop := st.plans[0], st.plans[1]
	if brew.Geo != "at" || brew.CMP != "complianz" || brew.Pages != 3 || len(brew.Targets) != 2 || brew.Targets[1].URL.String() != "https://brew.example/menu/" ||
		brew.Targets[0].Site != "brew" || strings.Join(brew.Targets[0].Args, " ") != "--geo at --cmp complianz --pages=3" {
		t.Errorf("brew: %+v %+v", brew, brew.Targets)
	}
	if shop.Geo != "de" || shop.CMP != "cookiebot" || shop.Pages != 1 || len(shop.Targets) != 1 || shop.Targets[0].URL.String() != "https://shop.example/" {
		t.Errorf("shop: %+v", shop)
	}
}

func TestHeaderFileAndEnv(t *testing.T) {
	dir := inDir(t, "")
	token := "Bearer " + "tok_" + "file1234"
	putFile(t, "token.txt", "  "+token+"\n")
	t.Setenv("RHYTHM_ECHO_STAGING_AUTH", "Basic "+"c2VjcmV0")
	t.Setenv("RHYTHM_ECHO_SESSION", "s3ss")
	st := mustPrepare(t, "-H", "X-Token: @token.txt", "--header", "Authorization: env:RHYTHM_ECHO_STAGING_AUTH",
		"--cookie", "session=env:RHYTHM_ECHO_SESSION", "--cookie", "lang=de", "https://shop.test/")
	p := st.plans[0]
	if p.Headers["X-Token"] != token || p.Headers["Authorization"] != "Basic c2VjcmV0" || strings.Join(p.Cookies, "; ") != "session=s3ss; lang=de" {
		t.Errorf("headers %v cookies %v", p.Headers, p.Cookies)
	}
	_, err := prepareArgs(t, "-H", "Authorization: env:RHYTHM_ECHO_MISSING", "https://shop.test/")
	if err == nil || err.Error() != "--header Authorization: env:RHYTHM_ECHO_MISSING is not set" {
		t.Errorf("a missing variable: %v", err)
	}
	putFile(t, "big.txt", strings.Repeat("x", 64<<10+1))
	if _, err := prepareArgs(t, "-H", "X: @big.txt", "https://shop.test/"); err == nil || err.Error() != "--header X: @big.txt is more than 64 KiB" {
		t.Errorf("a big file: %v", err)
	}
	if _, err := prepareArgs(t, "-H", "X: @"+filepath.Join(dir, "nope"), "https://shop.test/"); err == nil || !strings.Contains(err.Error(), "nope doesn't exist") {
		t.Errorf("a missing file: %v", err)
	}
	secret := "hunter" + "2"
	if _, err := prepareArgs(t, "-H", "no colon "+secret, "https://shop.test/"); err == nil || strings.Contains(err.Error(), secret) {
		t.Errorf("a header without a colon: %v", err)
	}
	if _, err := prepareArgs(t, "--cookie", "novalue", "https://shop.test/"); err == nil || err.Error() != `--cookie: want name=value, e.g. --cookie "session=env:RHYTHM_ECHO_SESSION"` {
		t.Errorf("a cookie without a value: %v", err)
	}
	// consent: headers come from Echo's variables; a flag wins for the
	// same name.
	putFile(t, filepath.Join(".echo", "config.yaml"), "consent:\n  headers:\n    Authorization: ${env:RHYTHM_ECHO_STAGING_AUTH}\n    X-Other: ${env:RHYTHM_ECHO_SESSION}\n")
	p = mustPrepare(t, "-H", "X-Other: plain", "https://shop.test/").plans[0]
	if p.Headers["Authorization"] != "Basic c2VjcmV0" || p.Headers["X-Other"] != "plain" {
		t.Errorf("config headers: %v", p.Headers)
	}
	putFile(t, filepath.Join(".echo", "config.yaml"), "consent:\n  headers:\n    Authorization: ${env:RHYTHM_ECHO_UNSET_ONE}\n")
	if _, err := prepareArgs(t, "https://shop.test/"); err == nil || err.Error() != "consent: headers: Authorization: RHYTHM_ECHO_UNSET_ONE is not set" {
		t.Errorf("an unset config header: %v", err)
	}
}

// TestPlainHeaderWarns: message 22 once, however many plain values.
func TestPlainHeaderWarns(t *testing.T) {
	inDir(t, "")
	consenttest.Use(t, consenttest.Fakes())
	code, _, errOut := runMain(t, "-H", "A: one", "-H", "B: two", "--cookie", "c=1", "https://shop.test/")
	want := "rhythm echo: --header value on the command line shows in ps to every user on this machine; use @file or env:NAME\n"
	if strings.Count(errOut, want) != 1 || !strings.Contains(errOut, "rhythm echo: --cookie value on the command line shows in ps") {
		t.Errorf("exit %d\n%s", code, errOut)
	}
	t.Setenv("RHYTHM_ECHO_A", "one")
	if _, _, errOut := runMain(t, "-H", "A: env:RHYTHM_ECHO_A", "https://shop.test/"); strings.Contains(errOut, "shows in ps") {
		t.Errorf("env:NAME warned: %s", errOut)
	}
}

// TestSendHitsRefusedOnProduction: message 6, exit 2, before any
// browser starts.
func TestSendHitsRefusedOnProduction(t *testing.T) {
	inDir(t, "production: [shop.example.com]\n")
	runner := &consenttest.FakeRunner{Result: consenttest.GoldenResult("clean")}
	s := consenttest.Fakes()
	s.Runner = runner
	consenttest.Use(t, s)
	code, out, errOut := runMain(t, "--send-hits", "https://www.shop.example.com/")
	want := "rhythm echo: --send-hits on www.shop.example.com, which matches \"shop.example.com\" under production:. Echo holds tracker hits on production so your analytics stay clean; leave --send-hits off\n"
	if code != exitUsage || out != "" || errOut != want {
		t.Errorf("exit %d\n got %q\nwant %q", code, errOut, want)
	}
	if len(runner.Plans) != 0 {
		t.Error("the browser was started")
	}
	if _, err := os.Stat(".echo/consent"); err == nil {
		t.Error("a result folder was made")
	}
	// Held hits on production are the normal case.
	if _, err := prepareArgs(t, "https://shop.example.com/"); err != nil {
		t.Errorf("held on production: %v", err)
	}
	if _, err := prepareArgs(t, "--send-hits", "https://staging.shop.test/"); err != nil {
		t.Errorf("--send-hits on staging: %v", err)
	}
}

func TestShowIDsRefused(t *testing.T) {
	inDir(t, "")
	want := "--show-ids keeps tracking IDs unmasked; it can't be used with Markdown or JUnit output, which are meant to be shared"
	for _, args := range [][]string{{"--md"}, {"--junit"}, {"-o", "r.md"}, {"-o", "r.xml"}, {"--json", "-o", "r.md"}} {
		_, err := prepareArgs(t, append(append([]string{"--show-ids"}, args...), "https://shop.test/")...)
		if err == nil || err.Error() != want {
			t.Errorf("--show-ids %v: %v", args, err)
		}
	}
	for _, args := range [][]string{{}, {"--json"}, {"-o", "r.json"}, {"--html"}} {
		if _, err := prepareArgs(t, append(append([]string{"--show-ids"}, args...), "https://shop.test/")...); err != nil {
			t.Errorf("--show-ids %v: %v", args, err)
		}
	}
	if code, _, errOut := runMain(t, "--show-ids", "--md", "https://shop.test/"); code != exitUsage || errOut != "rhythm echo: "+want+"\n" {
		t.Errorf("exit %d %q", code, errOut)
	}
}

func TestInsecurePublic(t *testing.T) {
	inDir(t, "")
	_, err := prepareArgs(t, "-k", "https://shop.example/")
	if want := "-k on https://shop.example/, a public site: evidence from an unverified connection proves little; add --insecure-public if you mean it"; err == nil || err.Error() != want {
		t.Errorf("-k on a public site: %v", err)
	}
	if code, _, errOut := runMain(t, "-k", "https://shop.example/"); code != exitUsage || !strings.HasPrefix(errOut, "rhythm echo: -k on https://shop.example/") {
		t.Errorf("exit %d %q", code, errOut)
	}
	for _, args := range [][]string{
		{"-k", "--insecure-public", "https://shop.example/"},
		{"-k", "https://localhost:8443/"}, {"-k", "https://127.0.0.1/"}, {"-k", "https://10.0.0.5/"}, {"-k", "https://[::1]:8443/"},
		{"-k", "https://staging.shop.test/"}, {"-k", "https://wp.local/"}, {"-k", "https://intranet/"},
		{"-k", "--map-host", "*.shop.example=127.0.0.1:18950", "https://www.shop.example/"},
	} {
		if _, err := prepareArgs(t, args...); err != nil {
			t.Errorf("%v: %v", args, err)
		}
	}
	if _, err := prepareArgs(t, "-k", "--map-host", "*.shop.example=8.8.8.8:443", "https://www.shop.example/"); err == nil {
		t.Error("-k on a site mapped to a public address was taken")
	}
	// --show-browser runs one browser at a time.
	if p := mustPrepare(t, "--show-browser", "--parallel", "6", "https://shop.test/").plans[0]; p.Parallel != 1 || !p.Browser.Visible {
		t.Errorf("--show-browser: parallel %d", p.Parallel)
	}
}

func TestMapHostAndEcho(t *testing.T) {
	inDir(t, "consent:\n  map_hosts:\n    \"*.shop.test\": 127.0.0.1:18950\n")
	p := mustPrepare(t, "--map-host", "tags.test=http://127.0.0.1:18951", "--map-host", "*.SHOP.test=127.0.0.1:18952", "--map-only", "https://a.shop.test/").plans[0]
	if p.MapHosts["*.shop.test"] != "127.0.0.1:18952" || p.MapHosts["tags.test"] != "http://127.0.0.1:18951" || !p.MapOnly {
		t.Errorf("map hosts: %v", p.MapHosts)
	}
	for _, bad := range []string{"x", "x=", "=1.2.3.4:5", "x=nowhere", "http://x/=1.2.3.4:5"} {
		_, err := prepareArgs(t, "--map-host", bad, "https://shop.test/")
		if want := `--map-host "` + bad + `": want host=address, e.g. *.shop.test=127.0.0.1:18950`; err == nil || err.Error() != want {
			t.Errorf("--map-host %q: %v", bad, err)
		}
	}
	t.Setenv("RHYTHM_ECHO_URL", "http://127.0.0.1:35101")
	t.Setenv("RHYTHM_ECHO_TOKEN", "tok")
	if p := mustPrepare(t, "--echo", "https://shop.test/").plans[0]; p.Echo == nil || p.Echo.URL != "http://127.0.0.1:35101" || p.Echo.Token != "tok" {
		t.Errorf("--echo: %+v", p.Echo)
	}
	if p := mustPrepare(t, "--echo=http://127.0.0.1:35102", "https://shop.test/").plans[0]; p.Echo == nil || p.Echo.URL != "http://127.0.0.1:35102" {
		t.Errorf("--echo=url: %+v", p.Echo)
	}
	t.Setenv("RHYTHM_ECHO_URL", "")
	if p := mustPrepare(t, "--echo", "https://shop.test/").plans[0]; p.Echo.URL != "http://127.0.0.1:8025" {
		t.Errorf("--echo default: %+v", p.Echo)
	}
}

func TestConfigErrorsAreUsage(t *testing.T) {
	inDir(t, "consent:\n  headers:\n    Authorization: Basic abc\n")
	code, _, errOut := runMain(t, "https://shop.test/")
	if code != exitUsage || !strings.HasSuffix(errOut, "config.yaml: line 3: consent: headers: Authorization: use ${env:RHYTHM_ECHO_NAME}, not the value itself; secrets never sit in the config\n") {
		t.Errorf("exit %d %q", code, errOut)
	}
	if code, _, errOut := runMain(t, "--config", "nope.yaml", "https://shop.test/"); code != exitUsage || !strings.HasPrefix(errOut, "rhythm echo: --config nope.yaml: ") {
		t.Errorf("--config missing: %d %q", code, errOut)
	}
	// With no URL the config's consent: urls are checked; a path needs
	// site:.
	putFile(t, filepath.Join(".echo", "config.yaml"), "site: https://staging.shop.test\nconsent:\n  urls: [/, https://other.test/x]\n")
	if p := mustPrepare(t).plans[0]; len(p.Targets) != 2 || p.Targets[0].URL.String() != "https://staging.shop.test/" {
		t.Errorf("consent: urls: %+v", p.Targets)
	}
}

// TestSitesFile: Soundcheck's sites file, with its own keys, loads;
// each site's consent.args apply to its pages only.
func TestSitesFile(t *testing.T) {
	inDir(t, "")
	putFile(t, "sites.yaml", `
- name: acme-shop
  url: https://staging.acme.test
  args: ["--limit", "5"]
  config: soundcheck.yml
- name: 9lives
  url: brew.test
  env_prefix: BREW
  consent:
    urls: [/, /menu/]
    args: ["--runs", "+gpc", "-H", "Authorization: env:RHYTHM_ECHO_BREW_AUTH"]
`)
	t.Setenv("RHYTHM_ECHO_BREW_AUTH", "Basic "+"YnJldw==")
	st := mustPrepare(t, "--sites", "sites.yaml")
	if len(st.plans) != 2 || st.pages() != 3 {
		t.Fatalf("plans %d pages %d", len(st.plans), st.pages())
	}
	acme, brew := st.plans[0], st.plans[1]
	if acme.Targets[0].URL.String() != "https://staging.acme.test/" || acme.Targets[0].Site != "acme-shop" || len(acme.Runs) != 3 || acme.Headers != nil {
		t.Errorf("acme: %+v", acme)
	}
	if brew.Targets[0].URL.String() != "https://brew.test/" || brew.Targets[1].URL.Path != "/menu/" || len(brew.Runs) != 4 || brew.Headers["Authorization"] != "Basic YnJldw==" {
		t.Errorf("brew: %+v", brew)
	}
	// Neighboring sites that would run the same way share one plan;
	// neither a site with its own args nor anything after it joins.
	putFile(t, "join.yaml", `
- {name: a, url: a.test}
- {name: b, url: b.test, consent: {urls: [/, /shop/]}}
- {name: c, url: c.test, consent: {args: [--geo, de]}}
- {name: d, url: d.test}
`)
	st = mustPrepare(t, "--sites", "join.yaml")
	if len(st.plans) != 3 || len(st.plans[0].Targets) != 3 || len(st.plans[1].Targets) != 1 || len(st.plans[2].Targets) != 1 {
		t.Fatalf("joined plans: %d", len(st.plans))
	}
	if st.plans[0].Targets[0].Site != "a" || st.plans[0].Targets[2].URL.Path != "/shop/" || st.plans[1].Geo != "de" || st.plans[2].Targets[0].Site != "d" {
		t.Errorf("joined plans: %+v %+v %+v", st.plans[0].Targets, st.plans[1], st.plans[2].Targets)
	}
	for yaml, want := range map[string]string{
		"":                                       "no sites listed",
		"[]":                                     "no sites listed",
		"sitez: []":                              `line 1: "sitez" isn't a key; did you mean sites?`,
		"- {name: a, url: a.test, envprefix: X}": `line 1: a site: "envprefix" isn't a key; did you mean env_prefix?`,
		"- {name: a, url: a.test, consent: {url: [/]}}":                                      `line 1: consent: "url" isn't a key; did you mean urls?`,
		"- {name: acme, url: a.test}\n- {name: Acme, url: b.test}":                           `site "Acme": the name is already used by site "acme"`,
		"- {name: ../acme, url: a.test}":                                                     `site "../acme": name must be letters, digits, dots, dashes and underscores (up to 64), starting with a letter or digit`,
		"- {url: a.test}":                                                                    "site 1: name must be",
		"- {name: acme}":                                                                     `site "acme": url is missing`,
		"- {name: acme, url: 'ftp://a.test'}":                                                `site "acme": url "ftp://a.test": want an http or https URL`,
		"- {name: a, url: a.test, consent: {args: [--send-hits]}}":                           `site "a": consent: args can't set --send-hits; only the command line can`,
		"- {name: a, url: a.test, consent: {args: [--browser, ./evil.sh]}}":                  `site "a": consent: args can't set --browser; only the command line can`,
		"- {name: a, url: a.test, consent: {args: [-o, x.json]}}":                            `site "a": consent: args can't set --output; only the command line can`,
		"- {name: a, url: a.test, consent: {args: [-k]}}":                                    `site "a": consent: args can't set --insecure; only the command line can`,
		"- {name: a, url: a.test, consent: {args: [--show-ids]}}":                            `site "a": consent: args can't set --show-ids; only the command line can`,
		"- {name: a, url: a.test, consent: {args: [/menu/]}}":                                `site "a": consent: args take flags only, not "/menu/"; list pages under consent: urls`,
		"- {name: a, url: a.test, consent: {args: ['-H', 'A: @/etc/passwd']}}":               `site "a": consent: args: header and cookie values must be env:RHYTHM_ECHO_NAME, never a file, the value itself or one of Echo's own variables`,
		"- {name: a, url: a.test, consent: {args: ['-H', 'X: env:RHYTHM_ECHO_TOKEN']}}":      "never a file, the value itself or one of Echo's own variables (RHYTHM_ECHO_TOKEN, RHYTHM_ECHO_URL and the like)",
		"- {name: a, url: a.test, consent: {args: ['-H', 'X: env:RHYTHM_ECHO_URL']}}":        "one of Echo's own variables",
		"- {name: a, url: a.test, consent: {args: ['--cookie', 'k=env:RHYTHM_ECHO_RELAY']}}": "one of Echo's own variables",
		"- {name: a, url: a.test, consent: {args: ['--cookie', 'k=env:HOME']}}":              `site "a": consent: args: header and cookie values must be env:RHYTHM_ECHO_NAME`,
		"- {name: a, url: a.test, consent: {args: [--pages, '11']}}":                         "--pages 11: want 0 to 10",
		"- {name: a, url: a.test, consent: {args: [--runs, decline]}}":                       `site "a": consent: args: --runs "decline"`,
		"- {name: a, url: a.test, consent: {urls: ['ftp://x']}}":                             `site "a": consent "ftp://x": want an http or https URL`,
	} {
		putFile(t, "bad.yaml", yaml)
		if _, err := prepareArgs(t, "--sites", "bad.yaml"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q:\n got %v\nwant %s", yaml, err, want)
		}
	}
	if _, err := prepareArgs(t, "--sites", "sites.yaml", "https://x.test/"); err == nil || err.Error() != "give pages or --sites, not both" {
		t.Errorf("pages and --sites: %v", err)
	}
	if _, err := prepareArgs(t, "--sites", "missing.yaml"); err == nil || !strings.HasPrefix(err.Error(), "--sites missing.yaml: ") {
		t.Errorf("a missing sites file: %v", err)
	}
}

// FuzzSitesFile: the sites file parser never panics; what it takes
// has names and URLs a check can use.
func FuzzSitesFile(f *testing.F) {
	f.Add("sites:\n  - name: brew\n    url: https://brew.example\n    consent:\n      urls: [/, /menu/]\n      args: [\"--geo\", \"de\"]\n")
	f.Add("- {name: a, url: a.test, env_prefix: A, args: [--limit, '5'], config: x.yml}\n")
	f.Add("- {name: a, url: a.test}\n- {name: A, url: b.test}\n")
	f.Add("sites: {name: a}\n")
	f.Add("- [a, b]\n")
	f.Add(":\n- -\n")
	f.Fuzz(func(t *testing.T, data string) {
		sites, err := parseSites([]byte(data))
		if err != nil {
			return
		}
		for _, s := range sites {
			if !siteName.MatchString(s.Name) || checkSiteURL(s.URL) != nil {
				t.Errorf("took %+v", s)
			}
		}
	})
}

// TestConsentPagesCap: --pages is held to 10 and a check to 50 pages.
func TestConsentPagesCap(t *testing.T) {
	inDir(t, "")
	if code, _, errOut := runMain(t, "--pages", "11", "https://shop.test/"); code != exitUsage || errOut != "rhythm echo: --pages 11: want 0 to 10\n" {
		t.Errorf("--pages 11: %d %q", code, errOut)
	}
	var b strings.Builder
	b.WriteString("sites:\n")
	for i := range 21 {
		b.WriteString("  - name: s" + string(rune('a'+i)) + "\n    url: https://s" + string(rune('a'+i)) + ".test\n    consent:\n      urls: [/, /a/, /b/]\n")
	}
	putFile(t, "sites.yaml", b.String())
	code, _, errOut := runMain(t, "--sites", "sites.yaml")
	if code != exitUsage || errOut != "rhythm echo: 63 pages is more than 50 for one check; split the sites file\n" {
		t.Errorf("63 pages: %d %q", code, errOut)
	}
}

// TestPlanUsageMessages pins the exit-2 messages that need a config, a
// store or a file: PRD 10.2 messages 5, 6 and 7 among them.
func TestPlanUsageMessages(t *testing.T) {
	inDir(t, "production: [shop.example.com]\n")
	useRunner(t, consenttest.GoldenResult("clean"), nil)
	putFile(t, "two.yaml", "- {name: a, url: a.test, consent: {args: [--send-hits]}}\n")
	cases := [][]string{
		{"--against", "baseline", "https://shop.test/"},
		{"--against", "last", "https://shop.test/"},
		{"--send-hits", "https://shop.example.com/"},
		{"--show-ids", "--junit", "https://shop.test/"},
		{"-k", "https://shop.example/"},
		{"-H", "Authorization: env:RHYTHM_ECHO_NOT_SET", "https://shop.test/"},
		{"--map-host", "x", "https://shop.test/"},
		{"--sites", "two.yaml"},
		{"--sites", "two.yaml", "https://shop.test/"},
		{"--save", "a b", "https://shop.test/"},
		{"/shop/"},
	}
	var b strings.Builder
	for _, args := range cases {
		code, out, errOut := runMain(t, args...)
		if code != exitUsage || out != "" {
			t.Errorf("%v: exit %d, stdout %q", args, code, out)
		}
		b.WriteString("$ consent " + strings.Join(args, " ") + "\n" + errOut)
	}
	putFile(t, filepath.Join(".echo", "config.yaml"), "consent:\n  headers:\n    Authorization: Basic abc\n")
	_, _, errOut := runMain(t, "https://shop.test/")
	b.WriteString("$ consent https://shop.test/ (a literal header in the config)\n" + strings.Replace(filepath.ToSlash(errOut), filepath.ToSlash(mustWd(t))+"/.echo", "<dir>/.echo", 1))
	golden(t, "plan-usage-messages", b.String())
}

// mustWd is the working folder.
func mustWd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return wd
}

// A --sign-key that can't sign is refused before anything runs.
func TestSignKeyChecked(t *testing.T) {
	inDir(t, "")
	runner, _ := useRunner(t, consenttest.GoldenResult("clean"), nil)
	putFile(t, "team.key", "not a key")
	code, _, errOut := runMain(t, "--sign-key", "team.key", "https://shop.test/")
	if code != exitUsage || errOut != "rhythm echo: --sign-key: want an ed25519 private key in PEM (openssl genpkey -algorithm ed25519 -out team.key)\n" || len(runner.Plans) != 0 {
		t.Errorf("exit %d %q", code, errOut)
	}
	if _, err := os.Stat(".echo"); err == nil {
		t.Error("a refused key made the store")
	}
}

// --proxy takes http, https or socks5 with a host; anything else is a
// usage error that shows the value masked.
func TestProxyFlag(t *testing.T) {
	inDir(t, "")
	for in, want := range map[string]string{
		"http://127.0.0.1:8080":   "http://127.0.0.1:8080",
		"HTTPS://proxy.eu.test/":  "https://proxy.eu.test",
		"socks5://[::1]:1080":     "socks5://[::1]:1080",
		" socks5://eu.test:1080 ": "socks5://eu.test:1080",
	} {
		if p := mustPrepare(t, "--proxy", in, "https://shop.test/").plans[0]; p.Proxy != want {
			t.Errorf("--proxy %q: %q, want %q", in, p.Proxy, want)
		}
	}
	secret := "pr0xy" + "-pass-" + "x7q"
	for _, in := range []string{
		"ftp://u:" + secret + "@x.test",
		"http://u:" + secret + "@x.test:8080",
		"socks5://" + secret + "@x.test",
		"127.0.0.1:8080",
		"proxy.eu.test",
		"http://",
		"http://x.test/path",
		"http://x.test/?k=" + secret,
		"socks4://x.test:1080",
		"http://x.test%zz",
	} {
		_, err := prepareArgs(t, "--proxy", in, "https://shop.test/")
		var ue kitcli.UsageError
		if !errors.As(err, &ue) {
			t.Errorf("--proxy %q: %v; want a usage error", in, err)
			continue
		}
		if strings.Contains(ue.Msg, secret) || !strings.Contains(ue.Msg, "--proxy") {
			t.Errorf("--proxy %q: message %q", in, ue.Msg)
		}
	}
}

// geoRunner is a runner that knows only de and gb, as the browser
// runner knows its own table.
type geoRunner struct{ consenttest.FakeRunner }

func (*geoRunner) KnowsGeo(cc string) bool {
	return strings.EqualFold(cc, "de") || strings.EqualFold(cc, "gb")
}

// A country the browser has no language and time zone for is refused
// before the check, from the flag, the config and a site's args alike;
// one it knows passes.
func TestGeoTheBrowserKnows(t *testing.T) {
	inDir(t, "")
	s := consenttest.Fakes()
	s.Runner = &geoRunner{}
	consenttest.Use(t, s)
	if p := mustPrepare(t, "--geo", "DE", "https://shop.test/").plans[0]; p.Geo != "de" {
		t.Errorf("geo %q", p.Geo)
	}
	want := `--geo "zz": Echo has no language and time zone for that country; use --locale and --timezone, or an EU or EEA country, GB, CH, US, CA, BR, AU, JP or IN`
	if _, err := prepareArgs(t, "--geo", "zz", "https://shop.test/"); err == nil || err.Error() != want {
		t.Errorf("--geo zz: %v", err)
	}
	putFile(t, ".echo/config.yaml", "consent:\n  geo: zz\n")
	if _, err := prepareArgs(t, "https://shop.test/"); err == nil || !strings.Contains(err.Error(), `consent: geo "zz": Echo has no language and time zone`) {
		t.Errorf("consent: geo zz: %v", err)
	}
	putFile(t, ".echo/config.yaml", "")
	putFile(t, "sites.yaml", "- {name: a, url: a.test, consent: {args: [--geo, zz]}}\n")
	if _, err := prepareArgs(t, "--sites", "sites.yaml"); err == nil || !strings.Contains(err.Error(), "Echo has no language and time zone") {
		t.Errorf("site args --geo zz: %v", err)
	}
}

// failingCatalog refuses the config's trackers as the real catalog
// does, its error already naming the key, or as a stage that doesn't.
type failingCatalog struct {
	consenttest.FakeClassifier
	err error
}

func (c *failingCatalog) AddCustom([]byte) error { return c.err }

// A refused custom tracker is a usage error that names consent:
// trackers once, whoever wrote the message.
func TestCustomTrackerErrorPrefix(t *testing.T) {
	inDir(t, "")
	putFile(t, ".echo/config.yaml", "consent:\n  trackers:\n    - id: acme\n")
	for _, msg := range []string{"consent: trackers: acme: no matches", "acme: no matches"} {
		s := consenttest.Fakes()
		s.Classifier = &failingCatalog{err: errors.New(msg)}
		consenttest.Use(t, s)
		_, err := prepareArgs(t, "https://shop.test/")
		if err == nil || err.Error() != "consent: trackers: acme: no matches" {
			t.Errorf("%q: %v", msg, err)
		}
	}
}
