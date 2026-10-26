// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cmp

import (
	"cmp"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/consent"
)

// rulesFS holds the compiled-in rule pack: rules/<id>.yaml plus the
// README that documents the format (the loader reads only .yaml).
//
//go:embed rules
var rulesFS embed.FS

// pollEvery is how often Detect and Choose look at the page again.
const pollEvery = 250 * time.Millisecond

// driver is the consent.CMPDriver: the rule pack and the code that
// finds, clicks and verifies with it.
type driver struct {
	rules     []*rule // by priority, then id; generic last
	byID      map[string]*rule
	info      consent.DataInfo
	globalsJS string // the main-world typeof check of every rule's globals

	// sleep waits between polls. Tests replace it to move a FakeTab's
	// clock instead of the wall clock.
	sleep func(ctx context.Context, d time.Duration) error
}

var _ consent.CMPDriver = (*driver)(nil)

func init() {
	d, err := load(rulesFS, "rules")
	if err != nil {
		// The rule pack is compiled in and TestLoadAll loads it, so a
		// broken one never ships; failing loudly beats a driver with
		// half its rules.
		panic(fmt.Sprintf("consent/cmp: %v", err))
	}
	consent.SetCMP(d)
}

// load reads every <dir>/*.yaml of fsys as a rule and builds the driver.
// Info().SHA256 is the SHA-256 of the rule files concatenated in name
// order and then generic_words.yaml, so `sha256sum` over the same files
// in the same order gives it.
func load(fsys fs.FS, dir string) (*driver, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("rules: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".yaml") {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	sum := sha256.New()
	d := &driver{byID: map[string]*rule{}, sleep: sleepCtx}
	for _, name := range names {
		data, err := fs.ReadFile(fsys, path.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("rules/%s: %w", name, err)
		}
		sum.Write(data)
		r, err := parseRule(name, data)
		if err != nil {
			return nil, err
		}
		if _, dup := d.byID[r.ID]; dup {
			return nil, lineError(name, 1, "id: %q is already a rule", r.ID)
		}
		d.byID[r.ID] = r
		d.rules = append(d.rules, r)
	}
	slices.SortFunc(d.rules, func(a, b *rule) int {
		// generic is the fallback whatever its priority says.
		if (a.ID == genericID) != (b.ID == genericID) {
			if a.ID == genericID {
				return 1
			}
			return -1
		}
		return cmp.Or(cmp.Compare(a.Priority, b.Priority), strings.Compare(a.ID, b.ID))
	})
	// The generic rule's words decide as much as a rule file does.
	sum.Write(genericWordsYAML)
	d.info = consent.DataInfo{Version: consent.DataVersion, SHA256: hex.EncodeToString(sum.Sum(nil)), Count: len(d.rules)}
	d.globalsJS = globalsScript(d.rules)
	return d, nil
}

// sleepCtx waits d or until ctx ends.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// tcfGlobal is the IAB TCF API every TCF CMP defines.
const tcfGlobal = "__tcfapi"

// globalsScript is the one main-world script Detect runs: a typeof test
// of each known global, built from the rule pack (names checked as
// identifiers by the loader), never from page data.
func globalsScript(rules []*rule) string {
	names := []string{tcfGlobal}
	for _, r := range rules {
		for _, g := range r.Globals {
			if !slices.Contains(names, g) {
				names = append(names, g)
			}
		}
	}
	list, _ := json.Marshal(names)
	return "// cmp-globals\n(() => { const o = {}; for (const n of " + string(list) +
		") { try { o[n] = typeof window[n] !== \"undefined\"; } catch (e) { o[n] = false; } } return o; })()"
}

// Info names the compiled-in rule pack.
func (d *driver) Info() consent.DataInfo { return d.info }

// Rules lists the rules as `consent cmps` shows them.
func (d *driver) Rules() []consent.CMPRuleInfo {
	out := make([]consent.CMPRuleInfo, 0, len(d.rules))
	for _, r := range d.rules {
		out = append(out, r.listing())
	}
	return out
}

// listing is the rule's row of `consent cmps`.
func (r *rule) listing() consent.CMPRuleInfo {
	if r.ID == genericID {
		return consent.CMPRuleInfo{
			ID: r.ID, Name: r.Name, DetectedBy: fmt.Sprintf("buttons whose text means accept or reject (%d languages)", len(genericLanguages)),
			Accept: "button", Reject: "first layer or settings", StateFrom: r.stateFrom(), Sources: slices.Clone(r.Sources),
		}
	}
	var by []string
	if len(r.Selectors) > 0 {
		l := r.Selectors[0]
		switch {
		case len(l.Shadow) > 0:
			by = append(by, l.Shadow[0]+" (shadow DOM)")
		case len(r.Banners) > 0 && len(r.Banners[0].Shadow) > 0 && r.Banners[0].Shadow[0] == l.CSS:
			// The host of a banner drawn in its shadow root.
			by = append(by, l.CSS+" (shadow DOM)")
		default:
			by = append(by, l.CSS)
		}
	}
	if len(r.Globals) > 0 {
		by = append(by, "window."+r.Globals[0])
	}
	if len(by) == 0 && len(r.Scripts) > 0 {
		by = append(by, r.Scripts[0])
	}
	accept := "button"
	if r.API.Accept != "" {
		accept += ", api"
	}
	reject := "first layer"
	if r.Second != nil {
		reject = "first layer or settings"
	}
	if r.API.Reject != "" {
		reject += ", api"
	}
	return consent.CMPRuleInfo{
		ID: r.ID, Name: r.Name, DetectedBy: strings.Join(by, ", "), Accept: accept, Reject: reject,
		StateFrom: r.stateFrom(), Sources: slices.Clone(r.Sources),
	}
}

// stateFrom says where the rule reads the stored choice, in the words
// of the STATE FROM column.
func (r *rule) stateFrom() string {
	v := r.Verify
	switch {
	case v.Parser == "banner":
		return "banner gone and stays gone"
	case len(v.Cookies) > 0 && strings.HasSuffix(v.Cookies[0], "*"):
		return v.Cookies[0] + " cookies"
	case len(v.Cookies) > 0:
		return v.Cookies[0] + " cookie"
	case len(v.Storage) > 0:
		return v.Storage[0] + " storage"
	case v.Parser == "tcf":
		return "TCF string"
	}
	return "CMP API"
}

// ruleFor returns the rule cmp names; nil when it names none (a banner
// found only through --accept-selector or --reject-selector).
func (d *driver) ruleFor(info consent.CMPInfo) *rule { return d.byID[info.ID] }

// candidates are the rules Detect tries, in order: the one --cmp names,
// or every named rule by priority and then generic.
func (d *driver) candidates(p *consent.Plan) ([]*rule, error) {
	want := "auto"
	if p != nil && p.CMP != "" {
		want = p.CMP
	}
	if want == "auto" {
		return d.rules, nil
	}
	r, ok := d.byID[want]
	if !ok {
		ids := make([]string, 0, len(d.rules))
		for _, r := range d.rules {
			ids = append(ids, r.ID)
		}
		return nil, fmt.Errorf("--cmp %q: want auto or one of %s", consent.SafeText(want, 40), strings.Join(ids, ", "))
	}
	return []*rule{r}, nil
}

// State reads the CMP's stored choice with the rule's verify spec; a
// rule judged by its banner reports Decided when the banner is gone.
func (d *driver) State(ctx context.Context, t consent.Tab, info consent.CMPInfo) (*consent.CMPState, error) {
	r := d.ruleFor(info)
	if r == nil {
		return nil, nil
	}
	if r.Verify.Parser == "banner" {
		showing, err := d.BannerShowing(ctx, t, info)
		if err != nil {
			return nil, err
		}
		return &consent.CMPState{CMP: r.ID, Source: "banner", Decided: !showing}, nil
	}
	return readState(ctx, t, r)
}

// The TC string every TCF CMP stores and the source the state reader
// gives it.
const (
	tcfCookie = "euconsent-v2"
	tcfSource = "tcf"
)

// readState asks the linked state reader for the rule's stored choice.
// A stored value it can't read comes back as an undecided state with an
// error: that is a choice not verified, not a failed run. A TCF rule
// whose own state is undecided is read again from the TC string.
func readState(ctx context.Context, t consent.Tab, r *rule) (*consent.CMPState, error) {
	sr := consent.Linked().State
	if sr == nil {
		return nil, consent.Need(consent.StageState)
	}
	st, err := sr.Read(ctx, t, r.Verify)
	if err != nil && (st == nil || ctx.Err() != nil) {
		return nil, err
	}
	if !r.TCF || (st != nil && st.Decided) {
		return st, nil
	}
	tc, terr := sr.Read(ctx, t, consent.VerifySpec{Parser: "tcf", Cookies: []string{tcfCookie}})
	if terr == nil && tc != nil && tc.Decided {
		return tc, nil
	}
	return st, nil
}
