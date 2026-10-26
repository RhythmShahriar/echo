// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cmp

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
)

// replicaRules are the named rules that ship replica pages, each with
// its expect.yaml.
func replicaRules(t *testing.T) (*driver, []*rule) {
	t.Helper()
	d, err := load(rulesFS, "rules")
	if err != nil {
		t.Fatal(err)
	}
	var out []*rule
	for _, r := range d.rules {
		if r.ID != genericID {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		t.Fatal("no rules with replica pages")
	}
	return d, out
}

// replicaPage builds the fake page of one expect.yaml row: what the
// scan sees, then the banner and controls from banner_after_ms on.
func replicaPage(d *driver, r *rule, row expectPage) *fakePage {
	pg := newFakePage()
	pg.drive(d)
	for _, g := range row.Sees.Globals {
		pg.globals[g] = true
	}
	pg.scripts = row.Sees.Scripts
	show := func(pg *fakePage) {
		if row.Sees.Banner {
			pg.banner(r.ID, true, row.Sees.Selectors...)
		}
		for _, c := range row.Sees.Controls {
			pg.show(c, c)
		}
		for _, c := range row.Sees.Decoys {
			pg.show(c, c)
		}
	}
	if len(row.Sees.Selectors) > 0 {
		pg.banner(r.ID, false, row.Sees.Selectors...)
	}
	if row.BannerAfterMS > 0 {
		pg.at(row.BannerAfterMS, show)
	} else {
		show(pg)
	}
	pg.sync()
	return pg
}

func TestReplicaDetect(t *testing.T) {
	d, rules := replicaRules(t)
	for _, r := range rules {
		for _, row := range readExpect(t, r.ID).Pages {
			name := r.ID + "/" + row.Page
			pg := replicaPage(d, r, row)
			info, err := d.Detect(context.Background(), pg.tab, consenttest.NewPlan(t), 10*time.Second)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if info.Found != row.Found || info.Present != row.Present || info.DetectedBy != row.DetectedBy {
				t.Errorf("%s: found %v present %v by %q; want %v %v %q", name, info.Found, info.Present, info.DetectedBy, row.Found, row.Present, row.DetectedBy)
			}
			if row.Present && info.ID != r.ID {
				t.Errorf("%s: detected %q", name, info.ID)
			}
			if row.Found && info.BannerAtMS != row.BannerAfterMS {
				t.Errorf("%s: banner at %d ms; want %d", name, info.BannerAtMS, row.BannerAfterMS)
			}
			if row.BannerAfterMS > 0 {
				// Not within 2 s: the wait is what finds a late banner.
				early, _ := d.Detect(context.Background(), replicaPage(d, r, row).tab, consenttest.NewPlan(t), 2*time.Second)
				if early.Found {
					t.Errorf("%s: found within 2 s", name)
				}
			}
			if len(pg.tab.Clicked) != 0 {
				t.Errorf("%s: Detect clicked", name)
			}
		}
	}
}

// replicaState stores what each control of a rule does: Accept the
// rule's accepted categories, Reject its rejected ones, Save whatever
// the toggles say.
type replicaState struct {
	r     *rule
	pg    *fakePage
	fs    *consenttest.FakeState
	opens map[string]bool
	row   expectPage
}

func (s *replicaState) store(cats map[string]bool) {
	src := "cookie:" + s.r.Verify.Cookies[0]
	s.fs.State = &consent.CMPState{CMP: s.r.ID, Source: src, Decided: true, Categories: maps.Clone(cats)}
	s.pg.banner(s.r.ID, false)
}

func (s *replicaState) click(_ *consenttest.FakeTab, l consent.Locator) {
	key := locKey(l)
	switch {
	case slices.ContainsFunc(s.r.Accept, func(c chain) bool { return c.Raw == key }):
		s.store(s.r.Accepted)
	case slices.ContainsFunc(s.r.FirstLayer, func(c chain) bool { return c.Raw == key }):
		s.store(s.r.Rejected)
	case s.opens[key]:
		for _, c := range s.row.Sees.Settings {
			s.pg.show(c, "Save")
		}
		s.pg.toggles = nil
		for i, on := range s.row.Sees.Toggles {
			s.pg.toggles = append(s.pg.toggles, toggle{Path: fmt.Sprintf("#toggle-%d", i), On: on})
		}
		s.pg.sync()
	case strings.HasPrefix(key, "#toggle-"):
		var i int
		_, _ = fmt.Sscanf(key, "#toggle-%d", &i)
		s.pg.toggles[i].On = !s.pg.toggles[i].On
		s.pg.sync()
	case slices.Contains(s.row.Sees.Settings, key):
		cats := maps.Clone(s.r.Rejected)
		for _, tg := range s.pg.toggles {
			if tg.On {
				cats = maps.Clone(s.r.Accepted) // a switch left on: not a Reject
			}
		}
		s.store(cats)
	}
}

func TestReplicaChoose(t *testing.T) {
	d, rules := replicaRules(t)
	for _, r := range rules {
		opens := map[string]bool{}
		if r.Second != nil {
			for _, c := range r.Second.Open {
				opens[c.Raw] = true
			}
		}
		for _, row := range readExpect(t, r.ID).Pages {
			if !row.Found {
				continue
			}
			for _, k := range []consent.RunKind{consent.RunAccept, consent.RunReject} {
				name := fmt.Sprintf("%s/%s %s", r.ID, row.Page, k)
				pg := replicaPage(d, r, row)
				fs := &consenttest.FakeState{}
				consenttest.Use(t, consent.Stages{State: fs})
				p := consenttest.NewPlan(t)
				info, err := d.Detect(context.Background(), pg.tab, p, p.BannerWait)
				if err != nil || !info.Found {
					t.Fatalf("%s: detect %+v, %v", name, info, err)
				}
				want := row.AcceptLayer
				if k == consent.RunReject {
					want = row.RejectLayer
				}
				found, reason, err := d.Find(context.Background(), pg.tab, p, info, k)
				if err != nil || reason != consent.ReasonNone || found.Layer != want {
					t.Errorf("%s: Find %+v, %q, %v; want layer %d", name, found, reason, err, want)
					continue
				}
				s := &replicaState{r: r, pg: pg, fs: fs, opens: opens, row: row}
				pg.tab.OnClick = s.click
				ch, reason, err := d.Choose(context.Background(), pg.tab, p, info, k)
				if err != nil || reason != consent.ReasonNone || !ch.Verified || ch.Layer != want {
					t.Errorf("%s: Choose %+v, %q, %v; want verified at layer %d", name, ch, reason, err, want)
					continue
				}
				if first := clicked(pg)[0]; !strings.HasPrefix(ch.Selector, first) || !strings.HasPrefix(found.Selector, first) {
					t.Errorf("%s: Find said %q, Choose clicked %q first", name, found.Selector, first)
				}
				for _, c := range clicked(pg) {
					if slices.Contains(row.Sees.Decoys, c) {
						t.Errorf("%s: clicked the decoy %s", name, c)
					}
				}
				if k == consent.RunReject && want == 2 {
					for _, tg := range pg.toggles {
						if tg.On {
							t.Errorf("%s: a switch was left on: %+v", name, pg.toggles)
						}
					}
				}
			}
		}
	}
}

var (
	tokenRE  = regexp.MustCompile(`[#.][A-Za-z_][A-Za-z0-9_-]*`)
	scriptRE = regexp.MustCompile(`<script[^>]*\ssrc="([^"]+)"`)
	remoteRE = regexp.MustCompile(`(?i)(src|href)="(https?:)?//`)
)

// TestReplicaMarkup checks the pages say what their expect.yaml row
// says without a browser: every selector the row sees is in the page
// markup, a text-only page has none of the rule's markup or scripts,
// and no page loads anything from the network (the testbed serves them
// as they are).
func TestReplicaMarkup(t *testing.T) {
	_, rules := replicaRules(t)
	for _, r := range rules {
		for _, row := range readExpect(t, r.ID).Pages {
			name := r.ID + "/" + row.Page
			b, err := os.ReadFile(filepath.Join("testdata", r.ID, row.Page))
			if err != nil {
				t.Fatal(err)
			}
			html := string(b)
			seen := slices.Concat(row.Sees.Selectors, row.Sees.Controls, row.Sees.Settings, row.Sees.Decoys)
			for _, sel := range seen {
				for _, tok := range tokenRE.FindAllString(sel, -1) {
					attr := `id="` + tok[1:] + `"`
					if tok[0] == '.' {
						attr = tok[1:]
					}
					if !strings.Contains(html, attr) {
						t.Errorf("%s: %s (from %q) isn't in the markup", name, tok, sel)
					}
				}
			}
			var srcs []string
			for _, m := range scriptRE.FindAllStringSubmatch(html, -1) {
				srcs = append(srcs, m[1])
			}
			for _, sub := range row.Sees.Scripts {
				if !slices.ContainsFunc(srcs, func(s string) bool { return strings.Contains(s, sub) }) {
					t.Errorf("%s: no script loads %s", name, sub)
				}
			}
			if !row.Present {
				for _, sel := range r.SelRaw {
					for _, tok := range tokenRE.FindAllString(sel, -1) {
						if strings.Contains(html, `id="`+tok[1:]+`"`) || strings.Contains(html, `class="`+tok[1:]) {
							t.Errorf("%s: a page with no CMP has %s", name, tok)
						}
					}
				}
				if len(srcs) > 0 {
					t.Errorf("%s: a page with no CMP loads %v", name, srcs)
				}
			}
			if m := remoteRE.FindString(html); m != "" {
				t.Errorf("%s: loads from the network (%s)", name, m)
			}
		}
		filepath.WalkDir(filepath.Join("testdata", r.ID), func(path string, e os.DirEntry, err error) error {
			if err == nil && strings.HasSuffix(path, ".js") {
				b, _ := os.ReadFile(path)
				if regexp.MustCompile(`\b(fetch|XMLHttpRequest|sendBeacon|import)\b\s*\(`).Match(b) {
					t.Errorf("%s: a replica script makes requests", path)
				}
			}
			return nil
		})
	}
}
