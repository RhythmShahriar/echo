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
	return slices.Concat(emailCases, smsCases, chatCases, analyticsCases, pushCases, paymentCases, crmCases)
}

func allNegatives() []negative {
	return slices.Concat(emailNegatives, smsNegatives, chatNegatives, analyticsNegatives, pushNegatives, paymentNegatives, crmNegatives)
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

func TestProvidersListsEveryRecognizer(t *testing.T) {
	listed := Providers()
	tested := map[string]bool{}
	for _, c := range allCases() {
		tested[c.provider] = true
	}
	for p := range tested {
		if !slices.Contains(listed, p) {
			t.Errorf("Providers() is missing %q", p)
		}
	}
	core := map[string]bool{"twilio": true, "slack": true, "discord": true, "woocommerce": true}
	for _, p := range listed {
		if !tested[p] && !core[p] {
			t.Errorf("provider %q has no fixture", p)
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
	for _, p := range Providers() {
		if _, ok := names[p]; !ok {
			t.Errorf("no display name for %q", p)
		}
	}
	if Name("amazon-ses") != "Amazon SES" || Name("unknown-thing") != "unknown-thing" {
		t.Error("Name")
	}
}
