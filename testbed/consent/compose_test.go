// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package consenttestbed

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/echo/internal/browser"
	"github.com/RhythmShahriar/echo/testbed/consent/scenarios"
)

// compose is the part of docker-compose.yml the seal depends on.
type compose struct {
	Name     string `yaml:"name"`
	Networks map[string]struct {
		Internal bool `yaml:"internal"`
		IPAM     struct {
			Config []struct {
				Subnet string `yaml:"subnet"`
			} `yaml:"config"`
		} `yaml:"ipam"`
	} `yaml:"networks"`
	Services map[string]struct {
		Image    string    `yaml:"image"`
		Networks yaml.Node `yaml:"networks"`
		DNS      []string  `yaml:"dns"`
		Ports    []string  `yaml:"ports"`
		Command  []string  `yaml:"command"`
		User     string    `yaml:"user"`
		Build    yaml.Node `yaml:"build"`
		Profiles []string  `yaml:"profiles"`
		CapDrop  []string  `yaml:"cap_drop"`
		Security []string  `yaml:"security_opt"`
		Depends  map[string]struct {
			Required *bool `yaml:"required"`
		} `yaml:"depends_on"`
	} `yaml:"services"`
}

func loadCompose(t *testing.T) compose {
	t.Helper()
	data, err := os.ReadFile("docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	var c compose
	if err := yaml.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

// networksOf lists a service's networks, whether written as a list or
// a map.
func networksOf(n yaml.Node) []string {
	var out []string
	switch n.Kind {
	case yaml.SequenceNode:
		for _, c := range n.Content {
			out = append(out, c.Value)
		}
	case yaml.MappingNode:
		for i := 0; i < len(n.Content); i += 2 {
			out = append(out, n.Content[i].Value)
		}
	}
	slices.Sort(out)
	return out
}

// TestComposeIsSealed checks the shape the seal rests on: the sealed
// network has no gateway, the runner is on it alone with the testbed
// resolver, the resolver answers with the gate's fixed address, the
// gate image is pinned by digest and every published port is on
// 127.0.0.1 in 18950-18959.
func TestComposeIsSealed(t *testing.T) {
	c := loadCompose(t)
	if c.Name != "echo-consent-testbed" {
		t.Errorf("project name %q", c.Name)
	}
	if !c.Networks["sealed"].Internal {
		t.Error("the sealed network isn't internal: it would have a gateway")
	}
	for _, name := range []string{"runner", "sealcheck"} {
		s, ok := c.Services[name]
		if !ok {
			t.Fatalf("no %s service", name)
		}
		if got := networksOf(s.Networks); !slices.Equal(got, []string{"sealed"}) {
			t.Errorf("%s is on %v, want the sealed network only", name, got)
		}
		if !slices.Equal(s.DNS, []string{"10.254.250.53"}) {
			t.Errorf("%s resolves through %v, want the testbed resolver", name, s.DNS)
		}
	}
	for name, s := range c.Services {
		nets := networksOf(s.Networks)
		if name != "gate" && slices.Contains(nets, "egress") {
			t.Errorf("%s is on the egress network; only the gate may be", name)
		}
		if name != "gate" && len(s.Ports) > 0 {
			t.Errorf("%s publishes ports; only the gate does", name)
		}
	}
	dns := c.Services["dns"]
	if i := slices.Index(dns.Command, "-answer"); i < 0 || dns.Command[i+1] != "10.254.250.10" {
		t.Errorf("dns answers with %v, want the gate's 10.254.250.10", dns.Command)
	}
	gate := c.Services["gate"]
	if !regexp.MustCompile(`^nginx:1\.27-alpine@sha256:[0-9a-f]{64}$`).MatchString(gate.Image) {
		t.Errorf("gate image %q isn't nginx:1.27-alpine pinned by digest", gate.Image)
	}
	for _, p := range gate.Ports {
		host, _, _ := strings.Cut(p, ":")
		rest := strings.TrimPrefix(p, host+":")
		port, _ := strconv.Atoi(strings.SplitN(rest, ":", 2)[0])
		if host != "127.0.0.1" || port < 18950 || port > 18959 {
			t.Errorf("gate publishes %q; want 127.0.0.1 in 18950-18959", p)
		}
	}
	if u := c.Services["runner"].User; !strings.Contains(u, "TESTBED_UID") {
		t.Errorf("runner user %q: Chrome's sandbox needs a non-root user", u)
	}
}

// TestComposeHardening: every image pulled is pinned by digest, the
// resolver runs without root or capabilities, Chrome's sandbox runs
// under a seccomp profile (never unconfined) and the shop starts without
// the optional echo service.
func TestComposeHardening(t *testing.T) {
	c := loadCompose(t)
	digest := regexp.MustCompile(`^[a-z0-9./-]+:[\w.-]+@sha256:[0-9a-f]{64}$`)
	for name, s := range c.Services {
		if s.Build.Kind == 0 && !digest.MatchString(s.Image) {
			t.Errorf("%s image %q isn't pinned by digest", name, s.Image)
		}
		for _, o := range s.Security {
			if strings.Contains(o, "unconfined") {
				t.Errorf("%s runs with %s", name, o)
			}
		}
	}
	dns := c.Services["dns"]
	if !strings.Contains(dns.User, "TESTBED_UID") || !slices.Equal(dns.CapDrop, []string{"ALL"}) {
		t.Errorf("dns runs as %q dropping %v; want the testbed user and no capabilities", dns.User, dns.CapDrop)
	}
	runner := c.Services["runner"]
	if !slices.Contains(runner.Security, "seccomp=./runner/seccomp-chrome.json") {
		t.Errorf("runner security_opt %v; want the Chrome seccomp profile", runner.Security)
	}
	if !slices.Equal(c.Services["echo"].Profiles, []string{"echo"}) {
		t.Errorf("echo profiles %v; want [echo] so the testbed runs without it", c.Services["echo"].Profiles)
	}
	if d, ok := c.Services["shop"].Depends["echo"]; !ok || d.Required == nil || *d.Required {
		t.Error("shop must depend on echo with required: false")
	}
}

// TestSeccompProfile: the runner's profile is Docker's default (deny by
// default) with one extra rule, which allows only the namespace calls
// Chrome's sandbox needs.
func TestSeccompProfile(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("runner", "seccomp-chrome.json"))
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		DefaultAction string `json:"defaultAction"`
		Syscalls      []struct {
			Names    []string `json:"names"`
			Action   string   `json:"action"`
			Includes struct {
				Caps []string `json:"caps"`
			} `json:"includes"`
			Args []json.RawMessage `json:"args"`
		} `json:"syscalls"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	if p.DefaultAction != "SCMP_ACT_ERRNO" {
		t.Errorf("defaultAction %q; want SCMP_ACT_ERRNO", p.DefaultAction)
	}
	var open []string
	for _, s := range p.Syscalls {
		if s.Action == "SCMP_ACT_ALLOW" && len(s.Includes.Caps) == 0 && len(s.Args) == 0 {
			open = append(open, s.Names...)
		}
	}
	for _, name := range []string{"clone", "setns", "unshare"} {
		if !slices.Contains(open, name) {
			t.Errorf("%s isn't allowed: Chrome's sandbox can't start", name)
		}
	}
	for _, name := range []string{"mount", "bpf", "kexec_load", "init_module", "reboot"} {
		if slices.Contains(open, name) {
			t.Errorf("%s is allowed for everyone", name)
		}
	}
}

// TestRunnerPinsMatch: the runner's Chrome for Testing is the version
// and archive Echo pins in internal/browser.
func TestRunnerPinsMatch(t *testing.T) {
	df, err := os.ReadFile(filepath.Join("runner", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	arg := func(name string) string {
		m := regexp.MustCompile(`(?m)^ARG ` + name + `=(\S+)$`).FindSubmatch(df)
		if m == nil {
			t.Fatalf("runner/Dockerfile has no ARG %s", name)
		}
		return string(m[1])
	}
	if v := arg("CFT_VERSION"); v != browser.PinnedVersion() {
		t.Errorf("runner Chrome %s, Echo pins %s", v, browser.PinnedVersion())
	}
	pins, err := os.ReadFile(filepath.Join("..", "..", "internal", "browser", "cft_pins.go"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`"linux64":\s*\{SHA256: "([0-9a-f]{64})"`).FindSubmatch(pins)
	if m == nil {
		t.Fatal("no linux64 pin in internal/browser/cft_pins.go")
	}
	if sum := arg("CFT_SHA256"); sum != string(m[1]) {
		t.Errorf("runner SHA-256 %s, Echo pins %s", sum, m[1])
	}
}

// lockEntry is one line of wp/plugins.lock.
type lockEntry struct{ slug, version, sum string }

func readLock(t *testing.T) []lockEntry {
	t.Helper()
	f, err := os.Open(filepath.Join("wp", "plugins.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out []lockEntry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			t.Fatalf("plugins.lock: %q: want slug version sha256", line)
		}
		out = append(out, lockEntry{fields[0], fields[1], fields[2]})
	}
	return out
}

// TestPluginsLock: both real CMPs are pinned to a version and a SHA-256,
// and the mu-plugin runs the files they ship.
func TestPluginsLock(t *testing.T) {
	lock := readLock(t)
	var slugs []string
	for _, e := range lock {
		slugs = append(slugs, e.slug)
		if !regexp.MustCompile(`^\d+(\.\d+)+$`).MatchString(e.version) || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(e.sum) {
			t.Errorf("%s: version %q, sha256 %q", e.slug, e.version, e.sum)
		}
	}
	if !slices.Equal(slugs, []string{"complianz-gdpr", "cookie-law-info"}) {
		t.Errorf("plugins.lock pins %v", slugs)
	}
	mu, err := os.ReadFile(filepath.Join("wp", "mu-plugins", "consent-testbed.php"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"'complianz-gdpr/complianz-gpdr.php'", "'cookie-law-info/cookie-law-info.php'"} {
		if !strings.Contains(string(mu), f) {
			t.Errorf("the mu-plugin doesn't run %s", f)
		}
	}
}

// TestVariantsMatchMuPlugin: the Docker variants in scenarios.yaml and
// the mu-plugin's TB_VARIANTS list the same sites, scenarios and
// plugins.
func TestVariantsMatchMuPlugin(t *testing.T) {
	m, err := scenarios.Load(".")
	if err != nil {
		t.Fatal(err)
	}
	mu, err := os.ReadFile(filepath.Join("wp", "mu-plugins", "consent-testbed.php"))
	if err != nil {
		t.Fatal(err)
	}
	php := map[string]string{}
	for _, r := range regexp.MustCompile(`'([a-z0-9]+-wp)' => array\( '([a-z0-9]+)', '([a-z]+)' \)`).FindAllStringSubmatch(string(mu), -1) {
		php[r[1]] = r[2] + " " + r[3] + "-wp"
	}
	yml := map[string]string{}
	for _, v := range m.Variants {
		yml[v.Site] = v.Of + " " + v.CMP
	}
	if len(yml) == 0 || len(php) != len(yml) {
		t.Fatalf("scenarios.yaml has %d variants, the mu-plugin %d", len(yml), len(php))
	}
	for site, want := range yml {
		if php[site] != want {
			t.Errorf("%s: scenarios.yaml %q, mu-plugin %q", site, want, php[site])
		}
	}
}
