// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cmp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
)

// betaRule is a second rule for ordering tests, tried before acme.
var betaRule = strings.NewReplacer("id: acme", "id: beta", "name: Acme CMP", "name: Beta CMP", "priority: 20", "priority: 5",
	"acme-", "beta-", "Acme", "Beta", "cdn.acme.test", "cdn.beta.test").Replace(acmeRule)

func twoRules(t *testing.T) *driver {
	return testDriver(t, map[string]string{"acme.yaml": acmeRule, "beta.yaml": betaRule})
}

func TestDetectFound(t *testing.T) {
	d := twoRules(t)
	pg := newFakePage()
	pg.drive(d)
	pg.tab.Clock = 1200
	pg.globals["Acme"] = true
	pg.banner("acme", true, "#acme-banner")
	info, err := d.Detect(context.Background(), pg.tab, consenttest.NewPlan(t), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	want := consent.CMPInfo{ID: "acme", Name: "Acme CMP", Found: true, Present: true,
		DetectedBy: "selector:#acme-banner and global:Acme", BannerAtMS: 1200}
	if info != want {
		t.Fatalf("info = %+v\nwant   %+v", info, want)
	}
	if pg.count("cmp-scan") != 1 || pg.count("cmp-globals") != 1 {
		t.Errorf("returned after %d scans; want the first", pg.count("cmp-scan"))
	}
}

func TestDetectPresentNotFound(t *testing.T) {
	d := twoRules(t)
	pg := newFakePage()
	pg.drive(d)
	pg.globals["Acme"] = true
	pg.scripts = []string{"cdn.acme.test/cmp.js"}
	pg.sync()
	info, err := d.Detect(context.Background(), pg.tab, consenttest.NewPlan(t), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if info.Found || !info.Present || info.ID != "acme" || info.DetectedBy != "global:Acme and script:cdn.acme.test/cmp.js" {
		t.Fatalf("info = %+v", info)
	}
	if pg.tab.Now() != 2000 || pg.count("cmp-scan") != 9 {
		t.Errorf("polled %d times up to %d ms; want 9 up to 2000 (every 250 ms)", pg.count("cmp-scan"), pg.tab.Now())
	}
}

func TestDetectHiddenMarkup(t *testing.T) {
	d := twoRules(t)
	pg := newFakePage()
	pg.drive(d)
	pg.banner("acme", false, "#acme-banner")
	info, err := d.Detect(context.Background(), pg.tab, consenttest.NewPlan(t), time.Second)
	if err != nil || info.Found || !info.Present || info.DetectedBy != "selector:#acme-banner" {
		t.Fatalf("info = %+v, err %v", info, err)
	}
	if len(pg.tab.Clicked) != 0 {
		t.Error("Detect clicked")
	}
}

func TestDetectLate(t *testing.T) {
	for _, tc := range []struct {
		wait  time.Duration
		found bool
	}{{2 * time.Second, false}, {10 * time.Second, true}} {
		d := twoRules(t)
		pg := newFakePage()
		pg.drive(d)
		pg.globals["Acme"] = true
		pg.sync()
		pg.at(4000, func(pg *fakePage) { pg.banner("acme", true, "#acme-banner") })
		info, err := d.Detect(context.Background(), pg.tab, consenttest.NewPlan(t), tc.wait)
		if err != nil {
			t.Fatal(err)
		}
		if info.Found != tc.found || !info.Present {
			t.Errorf("wait %s: info = %+v", tc.wait, info)
		}
		if tc.found && info.BannerAtMS != 4000 {
			t.Errorf("BannerAtMS = %d; want 4000", info.BannerAtMS)
		}
	}
}

func TestDetectNothing(t *testing.T) {
	// A page that only names the CMP in its text: nothing the scan reads.
	d := twoRules(t)
	pg := newFakePage()
	pg.drive(d)
	info, err := d.Detect(context.Background(), pg.tab, consenttest.NewPlan(t), time.Second)
	if err != nil || info != (consent.CMPInfo{}) {
		t.Fatalf("info = %+v, err %v", info, err)
	}
}

func TestDetectOrder(t *testing.T) {
	gen := strings.NewReplacer("id: acme", "id: generic", "priority: 20", "priority: 0", "acme-", "gen-").Replace(acmeRule)
	d := testDriver(t, map[string]string{"acme.yaml": acmeRule, "beta.yaml": betaRule, "generic.yaml": gen})
	pg := newFakePage()
	pg.drive(d)
	pg.banner("acme", true, "#acme-banner")
	pg.banner("beta", true, "#beta-banner")
	pg.banner("generic", true)
	p := consenttest.NewPlan(t)
	info, _ := d.Detect(context.Background(), pg.tab, p, time.Second)
	if info.ID != "beta" {
		t.Errorf("auto found %q; want beta (priority 5)", info.ID)
	}
	p.CMP = "acme"
	info, _ = d.Detect(context.Background(), pg.tab, p, time.Second)
	if info.ID != "acme" {
		t.Errorf("--cmp acme found %q", info.ID)
	}
	pg.banner("acme", false)
	pg.banner("beta", false)
	info, _ = d.Detect(context.Background(), pg.tab, p, time.Second)
	if info.Found {
		t.Errorf("--cmp acme fell back to %q", info.ID)
	}
	p.CMP = "auto"
	info, _ = d.Detect(context.Background(), pg.tab, p, time.Second)
	if info.ID != "generic" || info.DetectedBy != "generic" {
		t.Errorf("auto found %+v; want generic last", info)
	}
	if _, err := d.Detect(context.Background(), pg.tab, &consent.Plan{CMP: "nope"}, time.Second); err == nil {
		t.Error("an unknown --cmp was taken")
	}
}

func TestDetectScanInput(t *testing.T) {
	// --cmp limits the scan to that rule: the page is never asked about
	// the others.
	d := twoRules(t)
	rules, _ := d.candidates(&consent.Plan{CMP: "beta"})
	in := scanFor(rules)
	b, _ := json.Marshal(in)
	if strings.Contains(string(b), "acme") || !strings.Contains(string(b), `"key":"#beta-banner"`) || len(in.Scripts) != 1 {
		t.Fatalf("scan input = %s", b)
	}
}

func TestDetectTCFAndEvidenceCap(t *testing.T) {
	d := twoRules(t)
	pg := newFakePage()
	pg.drive(d)
	pg.globals["Acme"], pg.globals[tcfGlobal] = true, true
	pg.scripts = []string{"cdn.acme.test/cmp.js", "cdn.other.test/x.js"}
	pg.banner("acme", true, "#acme-banner")
	info, _ := d.Detect(context.Background(), pg.tab, consenttest.NewPlan(t), time.Second)
	if !info.TCF || info.DetectedBy != "selector:#acme-banner and global:Acme" {
		t.Fatalf("info = %+v", info)
	}
	pg.globals = map[string]bool{tcfGlobal: true}
	pg.rules, pg.scripts = map[string]scanRuleResult{}, nil
	pg.sync()
	info, _ = d.Detect(context.Background(), pg.tab, consenttest.NewPlan(t), 0)
	if !info.TCF || info.Present {
		t.Fatalf("TCF alone: %+v", info)
	}
}

func TestDetectBannerWithoutEvidence(t *testing.T) {
	d := twoRules(t)
	pg := newFakePage()
	pg.drive(d)
	pg.banner("acme", true)
	info, _ := d.Detect(context.Background(), pg.tab, consenttest.NewPlan(t), 0)
	if !info.Found || info.DetectedBy != "selector:#acme-banner" {
		t.Fatalf("info = %+v", info)
	}
}

func TestDetectSecondLayerOnly(t *testing.T) {
	only := strings.Replace(acmeRule, "  first_layer: [\"#acme-reject\"]\n", "", 1)
	d := testDriver(t, map[string]string{"acme.yaml": only})
	pg := newFakePage()
	pg.banner("acme", true, "#acme-banner")
	info, _ := d.Detect(context.Background(), pg.tab, consenttest.NewPlan(t), 0)
	if info.RejectLayer != 2 {
		t.Fatalf("RejectLayer = %d; want 2", info.RejectLayer)
	}
}

func TestDetectFrames(t *testing.T) {
	d := twoRules(t)
	pg := newFakePage()
	pg.drive(d)
	pg.tab.FrameList = []consent.Frame{{ID: "top", URL: "https://shop.test/"}, {ID: "f1", URL: "https://cmp.test/", CrossOrigin: true}}
	pg.banner("acme", true, "#acme-banner")
	if _, err := d.Detect(context.Background(), pg.tab, consenttest.NewPlan(t), 0); err != nil {
		t.Fatal(err)
	}
	if pg.count("cmp-scan") != 2 {
		t.Fatalf("scans = %d; want the top document and the cross-origin frame", pg.count("cmp-scan"))
	}
}

func TestDetectEvalErrors(t *testing.T) {
	d := twoRules(t)
	pg := newFakePage()
	d.sleep = func(context.Context, time.Duration) error { pg.tab.Advance(pollEvery); return nil }
	boom := errors.New("execution context was destroyed")
	pg.tab.Isolated["cmp-scan"] = consenttest.Answer{Err: boom}
	if _, err := d.Detect(context.Background(), pg.tab, consenttest.NewPlan(t), time.Second); !errors.Is(err, boom) {
		t.Fatalf("err = %v; want the last eval error when no poll worked", err)
	}

	// One failed poll (a navigation) and then the banner: found.
	pg = newFakePage()
	pg.drive(d)
	pg.tab.Isolated["cmp-scan"] = consenttest.Answer{Err: boom}
	d.sleep = func(context.Context, time.Duration) error {
		pg.tab.Advance(pollEvery)
		pg.banner("acme", true, "#acme-banner")
		return nil
	}
	info, err := d.Detect(context.Background(), pg.tab, consenttest.NewPlan(t), time.Second)
	if err != nil || !info.Found {
		t.Fatalf("info = %+v, err %v", info, err)
	}

	pg = newFakePage()
	pg.tab.Main["cmp-globals"] = consenttest.Answer{Result: json.RawMessage(`"not an object"`)}
	pg.banner("acme", true, "#acme-banner")
	if info, err := d.Detect(context.Background(), pg.tab, consenttest.NewPlan(t), 0); err != nil || !info.Found {
		t.Fatalf("an unreadable globals answer: %+v, %v", info, err)
	}
	pg.tab.Isolated["cmp-scan"] = consenttest.Answer{Result: json.RawMessage(`[1,2]`)}
	if _, err := d.Detect(context.Background(), pg.tab, consenttest.NewPlan(t), 0); err == nil {
		t.Fatal("an unreadable scan answer was taken")
	}
}

func TestDetectCanceled(t *testing.T) {
	d := twoRules(t)
	pg := newFakePage()
	pg.drive(d)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.Detect(ctx, pg.tab, consenttest.NewPlan(t), time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	d.sleep = sleepCtx
	if err := sleepCtx(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("sleepCtx = %v", err)
	}
	if err := sleepCtx(context.Background(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
}

func TestBannerShowing(t *testing.T) {
	d := twoRules(t)
	pg := newFakePage()
	info := consent.CMPInfo{ID: "acme"}
	for _, want := range []bool{true, false} {
		pg.banner("acme", want)
		got, err := d.BannerShowing(context.Background(), pg.tab, info)
		if err != nil || got != want {
			t.Errorf("BannerShowing = %v, %v; want %v", got, err, want)
		}
	}
	if got, _ := d.BannerShowing(context.Background(), pg.tab, consent.CMPInfo{}); got {
		t.Error("a banner without a rule shows")
	}
}

func TestScriptsAreConstant(t *testing.T) {
	d := twoRules(t)
	if !strings.HasPrefix(d.globalsJS, "// cmp-globals\n") || !strings.Contains(d.globalsJS, `["__tcfapi","Beta","Acme"]`) {
		t.Fatalf("globals script = %s", d.globalsJS)
	}
	if consenttest.ScriptName(scanJS) != "cmp-scan" || !strings.Contains(scanJS, "(input) => {") {
		t.Fatalf("scan script head = %.60s", scanJS)
	}
}
