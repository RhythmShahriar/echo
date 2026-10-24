// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package recognize

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/model"
)

// tcase is a request a recognizer must claim, what it must fill in, and
// the status its sealed fake must have.
type tcase struct {
	name     string
	provider string
	req      Request
	status   int
	check    func(t *testing.T, m model.Message)
}

// negative is a request that must not be claimed by provider.
type negative struct {
	name     string
	provider string
	req      Request
}

func req(method, raw string, headers map[string]string, body string) Request {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	h := map[string]string{}
	for k, v := range headers {
		h[strings.ToLower(k)] = v
	}
	return Request{Method: method, URL: u, Headers: h, Body: []byte(body)}
}

func jsonReq(method, raw, body string, headers ...string) Request {
	h := map[string]string{"content-type": "application/json"}
	for i := 0; i+1 < len(headers); i += 2 {
		h[headers[i]] = headers[i+1]
	}
	return req(method, raw, h, body)
}

func formReq(raw string, body string, headers ...string) Request {
	h := map[string]string{"content-type": "application/x-www-form-urlencoded"}
	for i := 0; i+1 < len(headers); i += 2 {
		h[headers[i]] = headers[i+1]
	}
	return req("POST", raw, h, body)
}

// join assembles key-shaped fixtures at run time, so the source holds no
// literal that secret scanners would flag.
func join(parts ...string) string { return strings.Join(parts, "") }

func allCases() []tcase {
	return slices.Concat(coreCases, emailCases, smsCases, chatCases, analyticsCases, pushCases, paymentCases, crmCases)
}

func allNegatives() []negative {
	return slices.Concat(coreNegatives, emailNegatives, smsNegatives, chatNegatives, analyticsNegatives, pushNegatives, paymentNegatives, crmNegatives)
}

func TestRecognizers(t *testing.T) {
	for _, c := range allCases() {
		t.Run(c.provider+"/"+c.name, func(t *testing.T) {
			var m model.Message
			rec := Recognize(c.req, &m)
			if rec.Provider != c.provider || m.Provider != c.provider {
				t.Fatalf("recognized as %q, want %q", rec.Provider, c.provider)
			}
			if c.check != nil {
				c.check(t, m)
			}
		})
	}
}

func TestRecognizersDontOverreach(t *testing.T) {
	for _, n := range allNegatives() {
		t.Run(n.provider+"/"+n.name, func(t *testing.T) {
			var m model.Message
			if rec := Recognize(n.req, &m); rec.Provider == n.provider {
				t.Errorf("%s %s claimed by %s", n.req.Method, n.req.URL, n.provider)
			}
		})
	}
}

// TestFakesAreWellFormed checks every sealed answer is something the
// provider's SDK would parse: the right status, a body that parses as its
// content type says, and a body wherever the SDK reads one.
func TestFakesAreWellFormed(t *testing.T) {
	for _, c := range allCases() {
		t.Run(c.provider+"/"+c.name, func(t *testing.T) {
			var m model.Message
			rec := Recognize(c.req, &m)
			f := FakeFor(rec, c.req, &m)
			if f.Status != c.status {
				t.Errorf("status %d, want %d (body %s)", f.Status, c.status, f.Body)
			}
			ct := f.Headers["content-type"]
			switch {
			case strings.Contains(ct, "json"):
				if !json.Valid([]byte(f.Body)) || f.Body == "" {
					t.Errorf("bad JSON body: %q", f.Body)
				}
			case strings.Contains(ct, "xml"):
				if !wellFormedXML(f.Body) {
					t.Errorf("bad XML body: %q", f.Body)
				}
			case ct == "":
				if f.Body != "" {
					t.Errorf("body without content type: %q", f.Body)
				}
			}
			if f.Status != 204 && f.Status != 202 && f.Body == "" && c.provider != "sendgrid" {
				t.Errorf("empty body with status %d", f.Status)
			}
		})
	}
}

func wellFormedXML(s string) bool {
	d := xml.NewDecoder(strings.NewReader(s))
	n := 0
	for {
		_, err := d.Token()
		if errors.Is(err, io.EOF) {
			return n > 0
		}
		if err != nil {
			return false
		}
		n++
	}
}

// TestEveryProviderHasFixtures enforces the fixture rule: each provider
// has a request it must claim and one it must leave alone.
func TestEveryProviderHasFixtures(t *testing.T) {
	pos, neg := map[string]int{}, map[string]int{}
	for _, c := range allCases() {
		pos[c.provider]++
	}
	for _, n := range allNegatives() {
		neg[n.provider]++
	}
	listed := Providers()
	for p := range pos {
		if !slices.Contains(listed, p) {
			t.Errorf("Providers() is missing %q", p)
		}
	}
	for _, p := range listed {
		if pos[p] < 1 || neg[p] < 1 {
			t.Errorf("provider %q has %d positive and %d negative fixtures, want at least one of each", p, pos[p], neg[p])
		}
	}
}

// TestNoFixtureIsClaimedTwice: each positive fixture matches the
// recognizers of one provider only, so the order in the registry is a
// tie-break that never decides a real case by accident.
func TestNoFixtureIsClaimedTwice(t *testing.T) {
	for _, c := range allCases() {
		var claimed []string
		for _, rec := range registry {
			if rec.Match(c.req) && !slices.Contains(claimed, rec.Provider) {
				claimed = append(claimed, rec.Provider)
			}
		}
		if len(claimed) != 1 {
			t.Errorf("%s/%s: %s %s matched by %q", c.provider, c.name, c.req.Method, c.req.URL, claimed)
		}
	}
}

// TestRegistryOrderIsExplicit: the registry is sorted by Priority then
// Provider, whatever order the init functions ran in.
func TestRegistryOrderIsExplicit(t *testing.T) {
	for i := 1; i < len(registry); i++ {
		a, b := registry[i-1], registry[i]
		if a.Priority < b.Priority || a.Priority == b.Priority && a.Provider > b.Provider {
			t.Errorf("%s (priority %d) before %s (priority %d)", a.Provider, a.Priority, b.Provider, b.Priority)
		}
	}
}

// eq fails the test when got != want.
func eq[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func eqList(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s = %q, want %q", what, got, want)
	}
}

func TestProvidersAreListedOnce(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range Providers() {
		if p == "" || seen[p] {
			t.Errorf("provider %q listed twice or empty", p)
		}
		seen[p] = true
	}
	if !seen["vonage"] {
		t.Error("vonage missing")
	}
}

func TestEveryProviderHasADisplayName(t *testing.T) {
	for _, r := range registry {
		if r.Name == "" {
			t.Errorf("no display name for %q", r.Provider)
		}
	}
	if Name("amazon-ses") != "Amazon SES" || Name("unknown-thing") != "unknown-thing" {
		t.Error("Name")
	}
}

func TestRedactURLMasksHookSecrets(t *testing.T) {
	for _, c := range []struct{ raw, want, secret string }{
		{zapierHook, "https://hooks.zapier.com/hooks/catch/1234567/ab•••••/", "c9xyz"},
		{"https://hook.eu2.make.com/" + join("abcd", "efgh1234ijkl5678"), "https://hook.eu2.make.com/ab•••••", "efgh1234"},
		{"https://shop.app.n8n.cloud/webhook/" + join("7f3a", "-9c2e"), "https://shop.app.n8n.cloud/webhook/7f•••••", "9c2e"},
		{"https://hooks.slack.com/services/T0000/B0000/" + join("XXXXXXXX", "abcdefgh"), "https://hooks.slack.com/services/T0000/B0000/XX•••••", "abcdefgh"},
		{"https://discord.com/api/webhooks/123/" + join("abcdefghij", "klmnopqrstuv"), "https://discord.com/api/webhooks/123/ab•••••", "klmnop"},
		{"https://prod-01.westus.logic.azure.com/workflows/1/triggers/manual/paths/invoke?api-version=2016-06-01&sig=" + join("Zx9", "secretsig"),
			"https://prod-01.westus.logic.azure.com/workflows/1/triggers/manual/paths/invoke?api-version=2016-06-01&sig=Zx•••••", "secretsig"},
		{"https://crm.shop.test/api/leads?page=2", "https://crm.shop.test/api/leads?page=2", ""},
	} {
		var m model.Message
		r := req("POST", c.raw, nil, "")
		got := RedactURL(Recognize(r, &m), c.raw)
		if got != c.want || (c.secret != "" && strings.Contains(got, c.secret)) {
			t.Errorf("RedactURL(%s) = %s, want %s", c.raw, got, c.want)
		}
	}
}
