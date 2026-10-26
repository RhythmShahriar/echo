// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package consent_test

import (
	"maps"
	"net/url"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
)

func TestPlanValidate(t *testing.T) {
	if err := consenttest.NewPlan(t).Validate(); err != nil {
		t.Fatalf("default plan: %v", err)
	}
	edge := consenttest.NewPlan(t)
	edge.Pages, edge.Settle, edge.BannerWait, edge.Timeout, edge.Retries, edge.Parallel = 10, 30*time.Second, 60*time.Second, 10*time.Minute, 3, 8
	if err := edge.Validate(); err != nil {
		t.Fatalf("every value at its limit: %v", err)
	}
	low := consenttest.NewPlan(t)
	low.Pages, low.Settle, low.BannerWait, low.Retries, low.Parallel = 0, 0, 0, 0, 1
	if err := low.Validate(); err != nil {
		t.Fatalf("every value at its floor: %v", err)
	}
	for _, c := range []struct {
		set  func(p *consent.Plan)
		want string
	}{
		{func(p *consent.Plan) { p.Pages = 11 }, "--pages 11: want 0 to 10"},
		{func(p *consent.Plan) { p.Pages = -1 }, "--pages -1: want 0 to 10"},
		{func(p *consent.Plan) { p.Settle = 45 * time.Second }, "--settle 45s: want at most 30s"},
		{func(p *consent.Plan) { p.Settle = -time.Second }, "--settle -1s: want 0 to 30s"},
		{func(p *consent.Plan) { p.Retries = 5 }, "--retries 5: want 0 to 3"},
		{func(p *consent.Plan) { p.Parallel = 9 }, "--parallel 9: want 1 to 8"},
		{func(p *consent.Plan) { p.Parallel = 0 }, "--parallel 0: want 1 to 8"},
		{func(p *consent.Plan) { p.BannerWait = 2 * time.Minute }, "--banner-wait 2m: want at most 60s"},
		{func(p *consent.Plan) { p.Timeout = 11 * time.Minute }, "--timeout 11m: want at most 10m"},
		{func(p *consent.Plan) { p.Timeout = 90*time.Minute + 30*time.Second }, "--timeout 1h30m30s: want at most 10m"},
		{func(p *consent.Plan) { p.Timeout = 2 * time.Hour }, "--timeout 2h: want at most 10m"},
		{func(p *consent.Plan) { p.Timeout = 0 }, "--timeout 0s: want more than 0 and at most 10m"},
		{func(p *consent.Plan) { p.Runs = nil }, "--runs: want at least one of none, accept, reject, gpc or withdraw"},
		{func(p *consent.Plan) { p.Runs = []consent.RunKind{"decline"} }, `--runs "decline": want none, accept, reject, gpc or withdraw (+gpc adds one)`},
		{func(p *consent.Plan) { p.Policy = "loose" }, `--consent-mode "loose": want advanced, basic or strict`},
		{func(p *consent.Plan) { p.Click = "mouse" }, `--click "mouse": want button, api or button,api`},
		{func(p *consent.Plan) {
			p.Targets = []consent.Target{{URL: &url.URL{Scheme: "ftp", Host: "shop.test", Path: "/"}}}
		},
			`consent "ftp://shop.test/": want an http or https URL`},
		{func(p *consent.Plan) { p.Targets = []consent.Target{{}} }, `consent "": want an http or https URL`},
	} {
		p := consenttest.NewPlan(t)
		c.set(p)
		err := p.Validate()
		if err == nil || err.Error() != c.want {
			t.Errorf("Validate() = %v; want %s", err, c.want)
		}
	}
}

func TestEffectiveDefault(t *testing.T) {
	denied := map[string]consent.Value{"ad_storage": consent.Denied, "analytics_storage": consent.Denied}
	granted := map[string]consent.Value{"ad_storage": consent.Granted, "analytics_storage": consent.Granted}
	eu := map[string]consent.Value{"ad_storage": consent.Denied, "analytics_storage": consent.Granted}
	sigs := []consent.Signal{
		{Source: "gtag", Command: "default", AtMS: 100, State: granted},
		{Source: "datalayer", Command: "default", AtMS: 110, State: eu, Region: []string{"DE", "FR"}},
		{Source: "gtag", Command: "update", AtMS: 120, State: granted},
		{Source: "fbq", Command: "default", AtMS: 130, State: granted},
	}
	check := func(name string, signals []consent.Signal, region string, want map[string]consent.Value, wantAt int64, wantOK bool) {
		t.Helper()
		got, at, ok := consent.EffectiveDefault(signals, region)
		if ok != wantOK || at != wantAt || !maps.Equal(got, want) {
			t.Errorf("%s: = %v, %d, %v; want %v, %d, %v", name, got, at, ok, want, wantAt, wantOK)
		}
	}
	check("region beats global", sigs, "de", eu, 110, true)
	check("global outside the region", sigs, "US", granted, 100, true)
	check("no region given", sigs, "", granted, 100, true)
	later := append(sigs[:2:2], consent.Signal{Source: "gtag", Command: "default", AtMS: 140, State: denied})
	check("last global wins", later, "US", denied, 140, true)
	check("region still beats a later global", later, "FR", eu, 110, true)
	twoRegion := append(later, consent.Signal{Source: "gtag", Command: "default", AtMS: 90, State: denied, Region: []string{"FR"}})
	check("last region-scoped by time wins", twoRegion, "FR", eu, 110, true)
	check("none", sigs[2:], "DE", nil, 0, false)
	check("empty", nil, "", nil, 0, false)

	// The returned map is a copy.
	got, _, _ := consent.EffectiveDefault(sigs, "")
	got["ad_storage"] = consent.Unknown
	if sigs[0].State["ad_storage"] != consent.Granted {
		t.Error("EffectiveDefault returned the signal's own map")
	}
}
