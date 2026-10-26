// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verdict

import (
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/consent"
)

func init() {
	consent.SetEvaluator(Evaluator{})
}

// Evaluator is the consent Evaluator: it fills each page's Consent Mode
// summary and NeedsNone, raises the issues of the catalog and lists the
// inconclusive runs. It keeps no state, so one value serves every check.
type Evaluator struct {
	// NowFunc is the clock ignore: until dates are read against (UTC
	// dates); nil is time.Now. Tests set it to pin "today".
	NowFunc func() time.Time
}

var _ consent.Evaluator = Evaluator{}

// Evaluate judges r under p. p may be nil (a stored result judged again):
// the result's own policy and geo are used then. Evaluate replaces
// r.Issues and r.Inconclusive, so judging a result twice gives the same.
func (e Evaluator) Evaluate(r *consent.Result, p *consent.Plan) {
	if r == nil {
		return
	}
	j := newJudge(r, p)
	j.now = e.NowFunc
	if j.now == nil {
		j.now = time.Now
	}
	r.Issues = []consent.Issue{}
	r.Inconclusive = []consent.Inconclusive{}
	j.clearNotes()
	j.markExempt()
	for i := range r.Pages {
		j.summarize(&r.Pages[i])
	}
	for i := range r.Pages {
		j.trackers(&r.Pages[i])
		j.consentMode(&r.Pages[i])
		j.choices(&r.Pages[i])
	}
	j.finish()
}

// judge holds what every check of one Evaluate call reads: the result,
// the plan's settings and the tracker names and needs.
type judge struct {
	r      *consent.Result
	p      *consent.Plan // nil when judged without a plan
	policy consent.Policy
	geo    string
	exempt map[string]bool
	info   map[string]consent.TrackerInfo // the catalog, when it is linked
	names  map[string]string              // tracker names seen in the result's hits
	now    func() time.Time               // the clock ignore: until dates are read against
}

func newJudge(r *consent.Result, p *consent.Plan) *judge {
	j := &judge{r: r, p: p, policy: r.Policy, geo: r.Geo, exempt: map[string]bool{},
		info: map[string]consent.TrackerInfo{}, names: map[string]string{}}
	if p != nil {
		j.policy, j.geo = p.Policy, p.Geo
		for _, id := range p.Exempt {
			j.exempt[strings.ToLower(strings.TrimSpace(id))] = true
		}
	}
	if j.policy == "" {
		j.policy = consent.PolicyAdvanced
	}
	if c := consent.Linked().Classifier; c != nil {
		for _, t := range c.Trackers() {
			j.info[t.ID] = t
		}
	}
	for _, pg := range r.Pages {
		for _, run := range pg.Runs {
			for _, h := range run.Hits {
				if h.Name != "" && j.names[h.Tracker] == "" {
					j.names[h.Tracker] = h.Name
				}
			}
		}
	}
	return j
}

// need is a tracker's consent need for entries that don't carry one
// (scripts). Exempt ids are exempt; a tracker neither the hits nor the
// catalog know counts as needed, so a missing catalog can't make a page
// look like it needs no consent.
func (j *judge) need(id string) consent.ConsentNeed {
	if j.exempt[id] {
		return consent.NeedExempt
	}
	if t, ok := j.info[id]; ok && t.Need != "" {
		return t.Need
	}
	for _, pg := range j.r.Pages {
		for _, run := range pg.Runs {
			for _, h := range run.Hits {
				if h.Tracker == id && h.Need != "" {
					return h.Need
				}
			}
		}
	}
	return consent.NeedNeeded
}

// runOf returns the page's run of kind k, or nil.
func runOf(pg *consent.Page, k consent.RunKind) *consent.Run {
	for i := range pg.Runs {
		if pg.Runs[i].Kind == k {
			return &pg.Runs[i]
		}
	}
	return nil
}

// googleTrackers are the catalog ids of Google's measurement and ads
// tags: their hits carry Consent Mode state. Tag Manager is a script
// only, so it is not one.
var googleTrackers = map[string]bool{"ga4": true, "universal-analytics": true, "google-ads": true,
	"floodlight": true, "google-remarketing": true, "google-ccm": true}

// googleHit reports whether h is a request hit of a Google tag.
func googleHit(h *consent.HitRef) bool {
	return googleTrackers[h.Tracker] && h.Kind != consent.HitScript
}
