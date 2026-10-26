// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cmp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
)

// fakePage is a page as the CMP scripts see it, answering a FakeTab:
// the globals, what the scan finds per rule, the controls the probe
// reports by locator key and the toggles of the settings. Timers change
// it as the fake clock moves (a banner injected after 4 s).
type fakePage struct {
	tab      *consenttest.FakeTab
	globals  map[string]bool
	rules    map[string]scanRuleResult
	scripts  []string
	controls map[string]probeResult
	toggles  []toggle
	timers   []fakeTimer
}

type fakeTimer struct {
	at int64
	do func(pg *fakePage)
}

func newFakePage() *fakePage {
	pg := &fakePage{
		tab:      &consenttest.FakeTab{Isolated: map[string]consenttest.Answer{}, Main: map[string]consenttest.Answer{}},
		globals:  map[string]bool{},
		rules:    map[string]scanRuleResult{},
		controls: map[string]probeResult{},
	}
	pg.sync()
	return pg
}

func answer(v any) consenttest.Answer {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return consenttest.Answer{Result: b}
}

// sync writes the page into the tab's scripted answers.
func (pg *fakePage) sync() {
	pg.tab.Main["cmp-globals"] = answer(pg.globals)
	pg.tab.Isolated["cmp-scan"] = answer(scanResult{Rules: pg.rules, Scripts: pg.scripts})
	pg.tab.Isolated["cmp-probe"] = answer(pg.controls)
	pg.tab.Isolated["cmp-toggles"] = answer(pg.toggles)
}

// banner shows or hides rule id's banner, matching sel.
func (pg *fakePage) banner(id string, visible bool, sel ...string) {
	r := pg.rules[id]
	r.Banner = visible
	if len(sel) > 0 {
		r.Matched = sel
	}
	pg.rules[id] = r
	pg.sync()
}

// show makes a control clickable with its text.
func (pg *fakePage) show(key, text string) {
	pg.controls[key] = probeResult{Found: true, Visible: true, Kind: true, Inside: true, Top: true, Text: text, Box: consent.Box{X: 10, Y: 20, W: 80, H: 30}}
	pg.sync()
}

// hide removes a control from the page.
func (pg *fakePage) hide(key string) {
	delete(pg.controls, key)
	pg.sync()
}

// at runs do once the clock reaches ms.
func (pg *fakePage) at(ms int64, do func(pg *fakePage)) {
	pg.timers = append(pg.timers, fakeTimer{at: ms, do: do})
}

func (pg *fakePage) fire() {
	now := pg.tab.Now()
	var left []fakeTimer
	for _, t := range pg.timers {
		if t.at <= now {
			t.do(pg)
		} else {
			left = append(left, t)
		}
	}
	pg.timers = left
	pg.sync()
}

// drive makes d sleep on the fake clock of pg.
func (pg *fakePage) drive(d *driver) {
	d.sleep = func(ctx context.Context, dur time.Duration) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		pg.tab.Advance(dur)
		pg.fire()
		return nil
	}
}

// count is how many times a script ran, by name.
func (pg *fakePage) count(name string) int {
	n := 0
	for _, e := range pg.tab.Evals {
		if e == name {
			n++
		}
	}
	return n
}

func testDriver(t *testing.T, files map[string]string) *driver {
	t.Helper()
	d, err := load(mapFS(files), "rules")
	if err != nil {
		t.Fatal(err)
	}
	return d
}
