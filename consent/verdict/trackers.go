// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verdict

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/RhythmShahriar/echo/consent"
)

// bucket is what part of a run an entry counts in for the tracker
// checks: before the choice, after Reject, after the withdraw Reject or
// the gpc run.
type bucket string

const (
	inBefore        bucket = "before"
	inAfterReject   bucket = "after-reject"
	inAfterWithdraw bucket = "after-withdraw"
	inGPC           bucket = "gpc"
)

// bucketOf maps a run kind and a phase to the bucket an entry counts
// in; false when it counts in none (Accept's after-accept phase: that is
// what the visitor allowed, queued hits flushed by the update included).
func bucketOf(k consent.RunKind, ph consent.Phase) (bucket, bool) {
	switch k {
	case consent.RunNone:
		return inBefore, true // the visitor never chose
	case consent.RunGPC:
		return inGPC, true
	}
	switch {
	case ph == consent.PhaseBefore:
		return inBefore, true
	case k == consent.RunReject && ph == consent.PhaseAfterReject:
		return inAfterReject, true
	case k == consent.RunWithdraw && ph == consent.PhaseAfterWithdraw:
		return inAfterWithdraw, true
	}
	return "", false
}

// tagManager is the catalog id of Google Tag Manager: a script only,
// which strict counts as a tracker script whatever its catalog need.
const tagManager = "google-tag-manager"

// metaPixel is Meta Pixel's catalog id.
const metaPixel = "meta-pixel"

// group is everything that makes one tracker issue: one check, run and
// tracker on one page.
type group struct {
	check, tracker string
	b              bucket   // the bucket the group was first seen in
	also           []bucket // later buckets of the same run, in order
	hits           []*consent.HitRef
	cookies        []*consent.Cookie
	storage        []*consent.StorageKey
	scripts        []*consent.Script
}

// groups keeps tracker issue groups in the order they were first seen,
// so issues come out the same on every run.
type groups struct {
	order []*group
	by    map[string]*group
}

func (gs *groups) get(check, tracker string, b bucket) *group {
	if gs.by == nil {
		gs.by = map[string]*group{}
	}
	k := check + "|" + tracker
	if g := gs.by[k]; g != nil {
		// One issue per check and tracker in a run (its key): a reject
		// run's cookieless pings before the choice and after Reject are
		// one group whose title names both.
		if !slices.Contains(g.also, b) && b != g.b {
			g.also = append(g.also, b)
		}
		return g
	}
	g := &group{check: check, tracker: tracker, b: b}
	gs.by[k] = g
	gs.order = append(gs.order, g)
	return g
}

// markExempt makes every hit, cookie and storage key of an --exempt
// tracker exempt, so the checks, the summary and the reports agree. A
// result judged again under a plan without that --exempt gets the
// catalog's need back, so dropping the flag brings the issues back;
// judged without a plan (nil), the stored needs stay as they are.
func (j *judge) markExempt() {
	if len(j.exempt) == 0 && j.p == nil {
		return
	}
	fix := func(id string, n *consent.ConsentNeed) {
		switch {
		case j.exempt[id]:
			*n = consent.NeedExempt
		case *n == consent.NeedExempt && j.p != nil:
			if t, ok := j.info[id]; ok && t.Need != "" {
				*n = t.Need
			}
		}
	}
	for i := range j.r.Pages {
		for k := range j.r.Pages[i].Runs {
			run := &j.r.Pages[i].Runs[k]
			for n := range run.Hits {
				fix(run.Hits[n].Tracker, &run.Hits[n].Need)
			}
			for n := range run.Cookies {
				fix(run.Cookies[n].Tracker, &run.Cookies[n].Need)
			}
			for n := range run.Storage {
				fix(run.Storage[n].Tracker, &run.Storage[n].Need)
			}
			for n := range run.Events {
				ev := &run.Events[n]
				switch {
				case ev.Hit != nil:
					fix(ev.Hit.Tracker, &ev.Hit.Need)
				case ev.Cookie != nil:
					fix(ev.Cookie.Tracker, &ev.Cookie.Need)
				case ev.Storage != nil:
					fix(ev.Storage.Tracker, &ev.Storage.Need)
				}
			}
		}
	}
}

// trackers raises the tracker checks of every run of pg: before-choice,
// after-reject, after-withdraw, embed, script-before, cookieless-ping and
// gpc-ignored, then meta-no-consent-api for the page.
func (j *judge) trackers(pg *consent.Page) {
	for i := range pg.Runs {
		run := &pg.Runs[i]
		var gs groups
		j.trackerHits(run, &gs)
		j.trackerStored(run, &gs)
		if j.policy == consent.PolicyStrict {
			j.trackerScripts(run, &gs)
		}
		for _, g := range gs.order {
			j.addGroup(pg, run, g)
		}
	}
	j.metaNoConsentAPI(pg)
}

// trackerHits files the run's request hits into groups.
func (j *judge) trackerHits(run *consent.Run, gs *groups) {
	for i := range run.Hits {
		h := &run.Hits[i]
		if h.Kind == consent.HitScript || !j.needed(h.Tracker, h.Need) {
			continue
		}
		b, ok := bucketOf(run.Kind, h.Phase)
		if !ok {
			continue
		}
		var check string
		switch {
		case b == inGPC:
			if h.Category != consent.CatAdvertising {
				continue
			}
			check = consent.CheckGPCIgnored
		case h.Category == consent.CatEmbed && b != inAfterWithdraw:
			check = consent.CheckEmbed
		case h.Cookieless && j.policy == consent.PolicyAdvanced:
			check = consent.CheckCookielessPing
		case b == inBefore:
			check = consent.CheckBeforeChoice
		case b == inAfterReject:
			if grantedGCS(h.Google) {
				continue // consent/reject-granted says it, not both
			}
			check = consent.CheckAfterReject
		default:
			check = consent.CheckAfterWithdraw
		}
		g := gs.get(check, h.Tracker, b)
		g.hits = append(g.hits, h)
	}
}

// trackerStored files the run's tracker cookies and storage keys into
// groups. Cookies the browser refused were never stored and don't
// count; in the gpc run only advertising cookies do.
func (j *judge) trackerStored(run *consent.Run, gs *groups) {
	check := func(cat consent.Category, b bucket, gpcCounts bool) string {
		switch {
		case b == inGPC:
			if !gpcCounts || cat != consent.CatAdvertising {
				return ""
			}
			return consent.CheckGPCIgnored
		case cat == consent.CatEmbed && b != inAfterWithdraw:
			return consent.CheckEmbed
		case b == inBefore:
			return consent.CheckBeforeChoice
		case b == inAfterReject:
			return consent.CheckAfterReject
		}
		return consent.CheckAfterWithdraw
	}
	for i := range run.Cookies {
		c := &run.Cookies[i]
		if c.Tracker == "" || c.Blocked != "" || !j.needed(c.Tracker, c.Need) {
			continue
		}
		if b, ok := bucketOf(run.Kind, c.Phase); ok {
			if id := check(c.Category, b, true); id != "" {
				g := gs.get(id, c.Tracker, b)
				g.cookies = append(g.cookies, c)
			}
		}
	}
	for i := range run.Storage {
		k := &run.Storage[i]
		if k.Tracker == "" || !j.needed(k.Tracker, k.Need) {
			continue
		}
		if b, ok := bucketOf(run.Kind, k.Phase); ok {
			if id := check(k.Category, b, false); id != "" {
				g := gs.get(id, k.Tracker, b)
				g.storage = append(g.storage, k)
			}
		}
	}
}

// trackerScripts files tracker scripts loaded before the choice or after
// Reject (strict only), Google Tag Manager included.
func (j *judge) trackerScripts(run *consent.Run, gs *groups) {
	for i := range run.Scripts {
		s := &run.Scripts[i]
		if j.exempt[s.Tracker] || (s.Tracker != tagManager && !j.needed(s.Tracker, "")) {
			continue
		}
		b, ok := bucketOf(run.Kind, s.Phase)
		if !ok || (b != inBefore && b != inAfterReject) {
			continue
		}
		g := gs.get(consent.CheckScriptBefore, s.Tracker, b)
		g.scripts = append(g.scripts, s)
	}
}

// grantedGCS reports whether g carries a known gcs with ad_storage or
// analytics_storage granted. An unknown gcs is never granted.
func grantedGCS(g *consent.GoogleConsent) bool {
	return g != nil && g.GCSDecoded != nil && g.GCSDecoded.Known &&
		(g.GCSDecoded.AdStorage == consent.Granted || g.GCSDecoded.AnalyticsStorage == consent.Granted)
}

// addGroup turns one group into its issue.
func (j *judge) addGroup(pg *consent.Page, run *consent.Run, g *group) {
	name := j.name(g.tracker)
	var title, fix string
	detail := entriesDetail(run, g)
	switch g.check {
	case consent.CheckBeforeChoice:
		title = name + " fired before the visitor chose"
	case consent.CheckAfterReject:
		title = name + " fired after Reject"
	case consent.CheckAfterWithdraw:
		title = name + " fired after consent was withdrawn"
	case consent.CheckEmbed:
		title = name + " loaded " + g.when("before the visitor chose")
	case consent.CheckScriptBefore:
		title = name + " script loaded " + g.when("before the visitor chose") + " (strict policy)"
	case consent.CheckCookielessPing:
		title = fmt.Sprintf("%s sent %s %s (allowed under --consent-mode advanced)", name, plural(len(g.hits), "cookieless ping"), g.when("before the choice"))
		detail = cookielessDetail(g)
	case consent.CheckGPCIgnored:
		title = name + " fired with Global Privacy Control on"
	}
	switch g.check {
	case consent.CheckBeforeChoice, consent.CheckAfterReject, consent.CheckAfterWithdraw, consent.CheckEmbed:
		fix = j.trackerFix(g.tracker, g.check)
	default:
		fix = checkFix(g.check)
	}
	j.add(pg, g.check, run.Kind, g.tracker, title, detail, fix, groupEvidence(run, g))
}

// when says when the group's entries happened, every bucket in the order
// seen: before names the before-the-choice bucket.
func (g *group) when(before string) string {
	var out []string
	for _, b := range append([]bucket{g.b}, g.also...) {
		switch b {
		case inAfterReject:
			out = append(out, "after Reject")
		case inAfterWithdraw:
			out = append(out, "after consent was withdrawn")
		default:
			out = append(out, before)
		}
	}
	return strings.Join(slices.Compact(out), " and ")
}

// plural writes n and a noun, with an s when n isn't 1.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// groupEvidence lists the event numbers behind a group, hits first.
func groupEvidence(run *consent.Run, g *group) []int64 {
	var ev []int64
	push := func(seq int64) {
		if seq > 0 && !slices.Contains(ev, seq) {
			ev = append(ev, seq)
		}
	}
	for _, h := range g.hits {
		push(h.Seq)
	}
	for _, c := range g.cookies {
		push(seqOfCookie(run, c))
	}
	for _, k := range g.storage {
		push(seqOfStorage(run, k))
	}
	for _, s := range g.scripts {
		push(s.Seq)
	}
	return ev
}

// maxPaths caps the page paths one detail lists.
const maxPaths = 5

// entriesDetail writes what a group recorded: the hit
// ("facebook.com/tr?id=••••5678&ev=PageView at 0.84s") or the hits
// ("3 hits (PageView on /, /shop/)"), then each tracker cookie and
// storage key ("cookie _fbp written"); a script group lists its script
// URLs.
func entriesDetail(run *consent.Run, g *group) string {
	var parts []string
	switch n := len(g.hits); {
	case n == 1:
		parts = append(parts, shortURL(g.hits[0].URL)+" at "+secs(g.hits[0].AtMS))
	case n > 1:
		parts = append(parts, fmt.Sprintf("%d hits (%s)", n, hitsWhere(run, g.hits)))
	}
	switch n := len(g.scripts); {
	case n == 1:
		parts = append(parts, shortURL(g.scripts[0].URL)+" at "+secs(g.scripts[0].AtMS))
	case n > 1:
		var urls []string
		for _, s := range g.scripts {
			if u := shortURL(s.URL); !slices.Contains(urls, u) {
				urls = append(urls, u)
			}
		}
		parts = append(parts, fmt.Sprintf("%d loads (%s)", n, strings.Join(urls, ", ")))
	}
	var seen []string
	for _, c := range g.cookies {
		if slices.Contains(seen, "c:"+c.Name) {
			continue
		}
		seen = append(seen, "c:"+c.Name)
		p := "cookie " + consent.SafeText(c.Name, 80) + " written"
		if len(parts) == 0 {
			p += " at " + secs(c.AtMS)
		}
		parts = append(parts, p)
	}
	for _, k := range g.storage {
		if slices.Contains(seen, "s:"+k.Area+k.Key) {
			continue
		}
		seen = append(seen, "s:"+k.Area+k.Key)
		p := k.Area + " storage key " + consent.SafeText(k.Key, 80) + " written"
		if len(parts) == 0 {
			p += " at " + secs(k.AtMS)
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, "; ")
}

// hitsWhere writes the events and pages of several hits: "PageView on
// /, /shop/", or "on /, /shop/" when the hits name no event.
func hitsWhere(run *consent.Run, hits []*consent.HitRef) string {
	var events, paths []string
	for _, h := range hits {
		for _, e := range h.Events {
			if e = consent.SafeText(e, 60); e != "" && !slices.Contains(events, e) && len(events) < 3 {
				events = append(events, e)
			}
		}
		if p := visitPath(run, h.Visit); p != "" && (len(paths) == 0 || paths[len(paths)-1] != p) {
			paths = append(paths, p)
		}
	}
	where := ""
	if len(paths) > maxPaths {
		paths = append(paths[:maxPaths], "...")
	}
	if len(paths) > 0 {
		where = "on " + strings.Join(paths, ", ")
	}
	if len(events) == 0 {
		return where
	}
	if where == "" {
		return strings.Join(events, ", ")
	}
	return strings.Join(events, ", ") + " " + where
}

// cookielessDetail writes a cookieless ping group: the endpoint with its
// gcs and the times, "g/collect?gcs=G100 at 0.62s and 9.90s; no Google
// cookie written".
func cookielessDetail(g *group) string {
	if len(g.hits) == 0 {
		return ""
	}
	h := g.hits[0]
	where := shortPath(h.URL)
	if h.Google != nil && h.Google.GCS != "" {
		where += "?gcs=" + consent.SafeText(h.Google.GCS, 16)
	}
	var times []string
	for i, x := range g.hits {
		if i == maxPaths {
			times = append(times, fmt.Sprintf("%d more", len(g.hits)-maxPaths))
			break
		}
		times = append(times, secs(x.AtMS))
	}
	return where + " at " + joinAnd(times) + "; no Google cookie written"
}

// keyParams are the query parameters a detail shows first: the ID of
// the property or pixel and the event.
var keyParams = []string{"id", "tid", "sdkid", "pid", "ev", "en", "event", "e", "ec", "ea"}

// skipParams never fill the two parameters of a detail: versions, client
// and session IDs, cache busters and parameters that carry whole URLs.
var skipParams = map[string]bool{"v": true, "_v": true, "cid": true, "uid": true, "sid": true, "_p": true, "_s": true,
	"z": true, "rnd": true, "random": true, "cb": true, "ord": true, "ts": true, "gtm": true, "_et": true,
	"dl": true, "dr": true, "dt": true, "ref": true, "rl": true, "url": true, "u": true, "page_location": true, "page_referrer": true}

// shortURL writes a (masked) URL as host and path with two key query
// parameters: "facebook.com/tr?id=••••5678&ev=PageView". The www. of the
// host is dropped.
func shortURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return consent.SafeText(raw, 200)
	}
	type kv struct{ k, v string }
	var all []kv
	for part := range strings.SplitSeq(u.RawQuery, "&") {
		k, v, _ := strings.Cut(part, "=")
		if k == "" {
			continue
		}
		if dk, err := url.QueryUnescape(k); err == nil {
			k = dk
		}
		if dv, err := url.QueryUnescape(v); err == nil {
			v = dv
		}
		all = append(all, kv{k, v})
	}
	pick := map[int]bool{}
	for _, want := range keyParams {
		for i, p := range all {
			if len(pick) < 2 && p.k == want && !pick[i] {
				pick[i] = true
				break
			}
		}
	}
	for i, p := range all {
		if len(pick) < 2 && !pick[i] && !skipParams[p.k] && len(p.v) <= 40 {
			pick[i] = true
		}
	}
	var q []string
	for i, p := range all {
		if pick[i] {
			q = append(q, p.k+"="+p.v)
		}
	}
	s := shortPath(raw)
	if len(q) > 0 {
		s += "?" + strings.Join(q, "&")
	}
	return consent.SafeText(s, 200)
}

// shortPath writes a URL as host (without www.) and path.
func shortPath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return consent.SafeText(raw, 200)
	}
	return consent.SafeText(strings.TrimPrefix(u.Host, "www.")+consent.ShownPath(u), 200)
}

// trackerChecks are the checks a Meta Pixel finding explains.
var trackerChecks = []string{consent.CheckBeforeChoice, consent.CheckAfterReject, consent.CheckAfterWithdraw}

// metaNoConsentAPI raises consent/meta-no-consent-api once per page: in
// the first run with a Meta Pixel error where no fbq('consent', ...)
// came before the pixel's first hit.
func (j *judge) metaNoConsentAPI(pg *consent.Page) {
	path := j.pageName(pg)
	for i := range pg.Runs {
		run := &pg.Runs[i]
		raised := slices.ContainsFunc(j.r.Issues, func(is consent.Issue) bool {
			return is.Page == path && is.Site == pg.Site && is.Run == run.Kind && is.Tracker == metaPixel &&
				slices.Contains(trackerChecks, is.Check)
		})
		if !raised {
			continue
		}
		first, at := firstMeta(run)
		if slices.ContainsFunc(run.Signals, func(s consent.Signal) bool {
			return s.Source == "fbq" && (s.Command == "revoke" || s.Command == "grant") && s.AtMS <= at
		}) {
			continue
		}
		var ev []int64
		if first != nil {
			ev = []int64{first.Seq}
		}
		detail := fmt.Sprintf("no fbq('consent', 'revoke') or fbq('consent', 'grant') before the first Meta Pixel hit at %s", secs(at))
		j.add(pg, consent.CheckMetaNoConsentAPI, run.Kind, metaPixel, "Meta Pixel never received fbq('consent', ...)",
			detail, checkFix(consent.CheckMetaNoConsentAPI), ev)
		return
	}
}

// firstMeta is the run's first Meta Pixel hit and its time; with no hit
// (a cookie alone), the first Meta Pixel cookie's time.
func firstMeta(run *consent.Run) (*consent.HitRef, int64) {
	var first *consent.HitRef
	for i := range run.Hits {
		h := &run.Hits[i]
		if h.Tracker == metaPixel && h.Kind != consent.HitScript && (first == nil || h.AtMS < first.AtMS) {
			first = h
		}
	}
	if first != nil {
		return first, first.AtMS
	}
	at := int64(-1)
	for _, c := range run.Cookies {
		if c.Tracker == metaPixel && (at < 0 || c.AtMS < at) {
			at = c.AtMS
		}
	}
	return nil, max(at, 0)
}
