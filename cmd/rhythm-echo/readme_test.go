// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/recognize"
)

func readme(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The README's config example must load as it stands, with an action in
// it: a reader copies it into .echo/config.yaml, so it can't drift from
// what config.Load accepts.
func TestReadmeConfigExampleLoads(t *testing.T) {
	blocks := regexp.MustCompile("(?s)```yaml\n(# \\.echo/config\\.yaml.*?)```").FindAllStringSubmatch(readme(t), -1)
	if len(blocks) == 0 {
		t.Fatal("README has no ```yaml block starting with # .echo/config.yaml")
	}
	for _, b := range blocks {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(b[1]), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := config.Load(path)
		if err != nil {
			t.Fatalf("README config example: %v\n%s", err, b[1])
		}
		if len(cfg.Actions) == 0 || cfg.Actions[0].HTTP == nil {
			t.Errorf("README config example should show an http action:\n%s", b[1])
		}
	}
}

// liveCharges are live-mode calls to the major payment gateways: one that
// got through could charge a real card.
var liveCharges = []struct{ gateway, method, url, auth string }{
	{"Stripe", "POST", "https://api.stripe.com/v1/payment_intents", "Bearer sk_" + "live_x"},
	{"PayPal", "POST", "https://api-m.paypal.com/v2/checkout/orders", "Bearer x"},
	{"Adyen", "POST", "https://x-checkout-live.adyenpayments.com/checkout/v71/payments", ""},
	{"Braintree", "POST", "https://api.braintreegateway.com/merchants/x/transactions", ""},
	{"Square", "POST", "https://connect.squareup.com/v2/payments", "Bearer x"},
	{"Mollie", "POST", "https://api.mollie.com/v2/payments", "Bearer live_x"},
	{"Authorize.net", "POST", "https://api.authorize.net/xml/v1/request.api", ""},
	{"Razorpay", "POST", "https://api.razorpay.com/v1/payments", "Basic cnpwX2xpdmVfeDp4"}, // rzp_live_x:x
}

// The README may promise that a test can never charge a card only while
// Echo refuses a live charge at every major gateway.
func TestReadmePaymentPromise(t *testing.T) {
	if !strings.Contains(readme(t), "never charge a card") {
		return
	}
	for _, c := range liveCharges {
		u, _ := url.Parse(c.url)
		r := recognize.Request{Method: c.method, URL: u, Headers: map[string]string{"authorization": c.auth}}
		if !recognize.Refuses(recognize.Recognize(r, &model.Message{}), r) {
			t.Errorf("README says a test can never charge a card but a live %s call (%s) isn't refused", c.gateway, c.url)
		}
	}
}

// The live charges above are what the promise is checked against, so at
// least Stripe and PayPal, which Echo refuses today, must count as refused.
func TestLiveChargesRefusedToday(t *testing.T) {
	for _, c := range liveCharges[:2] {
		u, _ := url.Parse(c.url)
		r := recognize.Request{Method: c.method, URL: u, Headers: map[string]string{"authorization": c.auth}}
		if !recognize.Refuses(recognize.Recognize(r, &model.Message{}), r) {
			t.Errorf("live %s call isn't refused", c.gateway)
		}
	}
}

// The README's count of the services Echo recognizes must be the
// catalog's.
func TestReadmeProviderCount(t *testing.T) {
	m := regexp.MustCompile(`(\d+) services are recognized out of the box`).FindStringSubmatch(readme(t))
	if m == nil {
		t.Fatal("README doesn't say how many services are recognized")
	}
	if want := strconv.Itoa(len(recognize.Catalog())); m[1] != want {
		t.Errorf("README says %s services are recognized; the catalog has %s", m[1], want)
	}
}

// consentSection is the README's cookie consent section, up to the next
// section.
func consentSection(t *testing.T) string {
	t.Helper()
	_, after, ok := strings.Cut(readme(t), "\n## Cookie consent checks\n")
	if !ok {
		t.Fatal("README has no ## Cookie consent checks section")
	}
	section, _, _ := strings.Cut(after, "\n## ")
	return section
}

// Every flag the README's consent section names is one consent or one
// of its subcommands takes, so an example can't drift from the CLI.
func TestReadmeConsentFlags(t *testing.T) {
	var help strings.Builder
	for _, args := range [][]string{{"consent", "--help"}, {"consent", "detect", "--help"}, {"consent", "report", "--help"},
		{"consent", "verify", "--help"}, {"consent", "cmps", "--help"}, {"consent", "trackers", "--help"}, {"consent", "prune", "--help"}} {
		code, out, errOut := runCLI(t, args...)
		if code != exitOK {
			t.Fatalf("%s: exit %d\n%s", strings.Join(args, " "), code, errOut)
		}
		help.WriteString(out)
	}
	known := map[string]bool{}
	for _, m := range regexp.MustCompile(`--[a-z][a-z-]*`).FindAllString(help.String(), -1) {
		known[m] = true
	}
	flags := regexp.MustCompile(`(?:^|[\s(`+"`"+`])(--[a-z][a-z-]*)`).FindAllStringSubmatch(consentSection(t), -1)
	if len(flags) < 10 {
		t.Fatalf("found %d flags in the README's consent section; the pattern no longer matches", len(flags))
	}
	for _, f := range flags {
		if !known[f[1]] {
			t.Errorf("README's consent section names %s, which consent's help doesn't list", f[1])
		}
	}
}

// The README's honest limits are consent.HonestLimits, the text the help
// and the report print.
func TestReadmeConsentLimits(t *testing.T) {
	section := consentSection(t)
	_, limits, ok := strings.Cut(section, "### Honest limits\n")
	if !ok {
		t.Fatal("README's consent section has no ### Honest limits")
	}
	var paras, cur []string
	for line := range strings.SplitSeq(limits, "\n") {
		switch {
		case strings.HasPrefix(line, "> "):
			cur = append(cur, strings.ReplaceAll(strings.TrimPrefix(line, "> "), "**", ""))
		case len(cur) > 0:
			paras = append(paras, strings.Join(cur, " "))
			cur = nil
		}
	}
	if got := strings.Join(paras, "\n\n"); got != consent.HonestLimits {
		t.Errorf("README's honest limits differ from consent.HonestLimits:\n got %q\nwant %q", got, consent.HonestLimits)
	}
	for _, want := range []string{"Hits are held in the browser: checking production adds nothing to anyone's analytics.",
		"Does Reject really reject?", "mode: consent"} {
		if !strings.Contains(section, want) {
			t.Errorf("README's consent section lacks %q", want)
		}
	}
}
