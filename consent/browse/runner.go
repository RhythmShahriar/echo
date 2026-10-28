// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browse

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/RhythmShahriar/echo/consent"
)

func init() { consent.SetRunner(Runner{}) }

// Runner is the consent Runner: it loads every page of a plan in
// Chrome with every run kind and records what happened (PRD 13.6). It
// never judges; the verdict stage does.
type Runner struct{}

var _ consent.Runner = Runner{}

// openWait bounds how long Chrome may take to open and set up a run's
// tab, within the run's own --timeout.
const openWait = 30 * time.Second

// engineStarted, when set (tests only), hears of every Engine a check
// starts, so a test can find its Chrome and profile.
var engineStarted func(*Engine)

// The causes that end a run early.
var (
	errRunTimeout = errors.New("the run took longer than --timeout")
	errRunLimit   = errors.New("the run reached the request limit")
)

// Run loads every target with every run kind through a pool of
// --parallel workers and returns the pages with their runs. A page
// whose every run hit a *consent.FatalError returns that error with the
// result. When ctx is canceled the runs that finished are kept, the rest
// are marked canceled and the partial result comes back with ctx.Err().
func (Runner) Run(ctx context.Context, p *consent.Plan) (*consent.Result, error) {
	started := time.Now()
	stages := consent.Linked()
	if err := consent.Need(consent.StageCMP); err != nil {
		return nil, err
	}
	e, err := Start(ctx, p)
	if err != nil {
		return nil, err
	}
	defer e.Close()
	if engineStarted != nil {
		engineStarted(e)
	}
	c := newCheck(e, p, stages)
	slots := c.runAll(ctx)
	res := c.result(started)
	var pageErr error
	for i, t := range p.Targets {
		pg, fatal := c.page(t, slots[i])
		res.Pages = append(res.Pages, pg)
		if fatal != nil && pageErr == nil {
			pageErr = fatal
		}
	}
	res.DurationMS = time.Since(started).Milliseconds()
	if err := ctx.Err(); err != nil {
		return res, err
	}
	if pageErr != nil {
		return res, pageErr
	}
	if c.writeErr != nil {
		return res, c.writeErr
	}
	return res, nil
}

// check is one consent command's state shared by its runs.
type check struct {
	e      *Engine
	plan   *consent.Plan
	stages consent.Stages
	echo   *echoLink // --echo; nil without

	mu       sync.Mutex
	notes    []string
	requests int
	held     int
	writeErr *consent.FatalError // the first evidence file that couldn't be written
}

// failedWrite keeps the first evidence file error: the check goes on,
// then ends with it, as its evidence is incomplete.
func (c *check) failedWrite(rel string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.writeErr == nil {
		c.writeErr = &consent.FatalError{Code: consent.FatalWrite, URL: rel, Detail: consent.MaskURL(err.Error(), false), Err: err}
	}
}

func newCheck(e *Engine, p *consent.Plan, stages consent.Stages) *check {
	return &check{e: e, plan: p, stages: stages, echo: newEchoLink(p.Echo)}
}

// note adds a result note once.
func (c *check) note(s string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !slices.Contains(c.notes, s) {
		c.notes = append(c.notes, s)
	}
}

// count adds one attempt's request counts to the check's.
func (c *check) count(rec *Recording) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests += rec.Requests
	c.held += rec.Held
}

// slot is where one run of one page ends up.
type slot struct {
	run   consent.Run
	info  *pageInfo
	fatal *consent.FatalError
	done  bool
}

// pageInfo is what a run learned about its page: where the start URL
// ended up and the CMP on it.
type pageInfo struct {
	final     string   // masked
	redirects []string // masked, the hops after the start URL
	cmp       consent.CMPInfo
}

// runAll runs every (page, run kind) pair, pages in target order, on
// --parallel workers (one with --show-browser).
func (c *check) runAll(ctx context.Context) [][]slot {
	p := c.plan
	slots := make([][]slot, len(p.Targets))
	for i := range slots {
		slots[i] = make([]slot, len(p.Runs))
	}
	workers := max(1, p.Parallel)
	if p.Browser.Visible {
		workers = 1
	}
	jobs := make(chan [2]int)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				if ctx.Err() != nil {
					continue
				}
				// Each worker writes only its own slot.
				slots[j[0]][j[1]] = c.runWithRetries(ctx, j[0], j[1])
			}
		}()
	}
feed:
	for i := range p.Targets {
		for k := range p.Runs {
			select {
			case jobs <- [2]int{i, k}:
			case <-ctx.Done():
				break feed
			}
		}
	}
	close(jobs)
	wg.Wait()
	return slots
}

// runWithRetries runs one page's run and, while it is inconclusive, runs
// it again in a fresh context up to --retries times. A run that recorded
// a consent-needing tracker before the choice or after Reject is never
// run again: that is a finding the verdict must see. A page that can't
// be checked at all (a fatal error) isn't retried either.
func (c *check) runWithRetries(ctx context.Context, page, k int) slot {
	var cur slot
	for n := 1; ; n++ {
		cur = c.attempt(ctx, page, k)
		cur.run.Attempts = n
		r := cur.run
		if cur.fatal != nil || ctx.Err() != nil || !r.Inconclusive() || r.Reason == consent.ReasonCanceled ||
			n > c.plan.Retries || hasFinding(r) {
			return cur
		}
	}
}

// hasFinding reports whether a run recorded a hit, cookie or storage key
// of a tracker that needs consent before the choice or after Reject.
func hasFinding(r consent.Run) bool {
	counts := func(ph consent.Phase, need consent.ConsentNeed) bool {
		return need == consent.NeedNeeded && (ph == consent.PhaseBefore || ph == consent.PhaseAfterReject)
	}
	for _, h := range r.Hits {
		if counts(h.Phase, h.Need) {
			return true
		}
	}
	for _, ck := range r.Cookies {
		if ck.Tracker != "" && counts(ck.Phase, ck.Need) {
			return true
		}
	}
	for _, s := range r.Storage {
		if s.Tracker != "" && counts(s.Phase, s.Need) {
			return true
		}
	}
	return false
}

// pageNumber is the number a page (index into the plan's targets) has
// in file names and Echo actions: page-1 is the first, after the pages
// earlier plans wrote into the same evidence folder.
func (c *check) pageNumber(page int) int { return c.plan.PageBase + page + 1 }

// attempt makes one run of page (index) with run kind k in a fresh
// context, bounded by --timeout.
func (c *check) attempt(ctx context.Context, page, k int) slot {
	p := c.plan
	t := p.Targets[page]
	kind := p.Runs[k]
	out := newRun(kind)
	started := time.Now()

	actx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	actx, stop := context.WithTimeoutCause(actx, p.Timeout, errRunTimeout)
	defer stop()

	n := c.pageNumber(page)
	a := &attempt{c: c, e: c.e, plan: p, target: t, page: n, kind: kind, out: &out}
	var x runExtras
	var events *eventFile
	if p.Dir != "" {
		var err error
		if events, err = openEventFile(p.Dir, n, kind); err != nil {
			c.failedWrite(eventsPath(n, kind), err)
		} else {
			x.sinks = append(x.sinks, events.write)
		}
	}
	es, runValue := c.echoBegin(actx, n, kind)
	x.echoRun = runValue
	run, err := c.e.open(actx, t, kind, openWait, x)
	if err != nil {
		c.echoEnd(ctx, es, a, nil)
		if events != nil {
			c.saveEvents(&out, events, nil)
		}
		a.ended(ctx, actx, err)
		out.DurationMS = time.Since(started).Milliseconds()
		return slot{run: out, done: true}
	}
	a.run, a.tab = run, run.Tab(n)
	go func() {
		select {
		case <-run.LimitReached():
			cancel(errRunLimit)
		case <-actx.Done():
		}
	}()
	fatal, err := a.steps(actx)
	rec := run.Close()
	c.count(rec)
	fill(&out, rec)
	out.Server = c.echoEnd(ctx, es, a, rec)
	if events != nil {
		c.saveEvents(&out, events, rec.Events)
	}
	if p.HAR && p.Dir != "" {
		if rel, err := writeHAR(p.Dir, n, kind, rec, p.Version, p.ShowIDs); err != nil {
			c.failedWrite(harPath(n, kind), err)
		} else {
			out.HARFile = rel
		}
	}
	if fatal == nil {
		a.ended(ctx, actx, err)
	}
	if rec.Limit && out.Reason == consent.ReasonNone {
		out.Status, out.Reason = consent.StatusLimit, consent.ReasonLimit
	}
	if out.Reason == consent.ReasonTimeout {
		if u := lastOpen(out.Events); u != "" {
			out.Detail = "the last request still open was " + u
		}
	}
	out.DurationMS = time.Since(started).Milliseconds()
	return slot{run: out, info: a.info, fatal: fatal, done: true}
}

// saveEvents closes a run's events file and names it in the run.
func (c *check) saveEvents(out *consent.Run, f *eventFile, final []consent.Event) {
	if err := f.close(final); err != nil {
		c.failedWrite(f.rel, err)
		return
	}
	out.EventsFile = f.rel
}

// newRun is an empty run of kind, its lists empty rather than null.
func newRun(kind consent.RunKind) consent.Run {
	return consent.Run{Kind: kind, Status: consent.StatusOK, Visits: []consent.Visit{}, Hits: []consent.HitRef{},
		Cookies: []consent.Cookie{}, Storage: []consent.StorageKey{}, Scripts: []consent.Script{}, Signals: []consent.Signal{},
		Screenshots: map[string]string{}}
}

// ended sets how a run that stopped with err ended: canceled when the
// command was, timeout or limit when the run's own bound fired, else a
// load failure with the masked error. A nil err changes nothing.
func (a *attempt) ended(parent, actx context.Context, err error) {
	out := a.out
	if err == nil {
		return
	}
	switch {
	case parent.Err() != nil:
		out.Status, out.Reason, out.Detail = consent.StatusCanceled, consent.ReasonCanceled, ""
	case errors.Is(context.Cause(actx), errRunLimit):
		out.Status, out.Reason = consent.StatusLimit, consent.ReasonLimit
	case errors.Is(context.Cause(actx), errRunTimeout):
		out.Status, out.Reason = consent.StatusTimeout, consent.ReasonTimeout
	default:
		out.Status, out.Reason = consent.StatusLoadFailed, consent.ReasonNavigation
		out.Detail = consent.SafeText(consent.MaskURL(err.Error(), false), 300)
	}
}

// fill copies what the recorder recorded into the run: the events and
// from them the hits, tracker scripts, cookies, storage keys and
// signals.
func fill(out *consent.Run, rec *Recording) {
	out.Events, out.Truncated, out.Dropped = rec.Events, rec.Truncated, rec.Dropped
	for _, e := range rec.Events {
		switch e.Kind {
		case consent.EvRequest:
			if e.Hit == nil || e.Request == nil {
				continue
			}
			if e.Hit.Kind == consent.HitScript {
				out.Scripts = append(out.Scripts, consent.Script{Tracker: e.Hit.Tracker, URL: e.Request.URL, AtMS: e.AtMS, Phase: e.Phase, Seq: e.Seq})
				continue
			}
			out.Hits = append(out.Hits, consent.HitRef{Seq: e.Seq, AtMS: e.AtMS, Phase: e.Phase, Visit: e.Visit, URL: e.Request.URL,
				Held: e.Request.Held, Hit: *e.Hit})
		case consent.EvCookie:
			if e.Cookie != nil {
				out.Cookies = append(out.Cookies, *e.Cookie)
			}
		case consent.EvStorage:
			if e.Storage != nil {
				out.Storage = append(out.Storage, *e.Storage)
			}
		case consent.EvSignal:
			if e.Signal != nil {
				out.Signals = append(out.Signals, *e.Signal)
			}
		}
	}
}

// lastOpen is the masked URL of the newest request that never got an
// answer, or "".
func lastOpen(events []consent.Event) string {
	best, at := "", int64(-1)
	for _, e := range events {
		r := e.Request
		if e.Kind != consent.EvRequest || r == nil || r.Status != 0 || r.Held || r.Refused != "" {
			continue
		}
		if e.AtMS >= at {
			best, at = r.URL, e.AtMS
		}
	}
	return consent.SafeText(best, 300)
}

// page puts a target's runs together. The page's final URL, redirects
// and CMP come from the first run in --runs order that loaded it. A run
// that couldn't load the page records why; when every run couldn't, the
// first one's fatal error is the page's.
func (c *check) page(t consent.Target, slots []slot) (consent.Page, *consent.FatalError) {
	shown := consent.MaskURL(t.URL.String(), c.plan.ShowIDs)
	pg := consent.Page{Site: t.Site, URL: shown, FinalURL: shown, Redirects: []string{}}
	var first *consent.FatalError
	fatal := 0
	var info *pageInfo
	for k, kind := range c.plan.Runs {
		s := slots[k]
		if !s.done {
			s.run = newRun(kind)
			s.run.Status, s.run.Reason = consent.StatusCanceled, consent.ReasonCanceled
		}
		if s.fatal != nil {
			fatal++
			if first == nil {
				first = s.fatal
			}
			s.run.Status, s.run.Reason = consent.StatusLoadFailed, consent.ReasonNavigation
			s.run.Detail = fatalText(s.fatal)
		}
		if info == nil && s.info != nil {
			info = s.info
		}
		pg.Runs = append(pg.Runs, s.run)
	}
	if info != nil {
		pg.FinalURL, pg.Redirects, pg.CMP = info.final, info.redirects, info.cmp
	}
	if fatal == len(c.plan.Runs) && fatal > 0 {
		return pg, first
	}
	return pg, nil
}

// fatalText is a fatal error as one line for a run's Detail, in the
// words of the PRD 10.2 messages.
func fatalText(f *consent.FatalError) string {
	var s string
	switch f.Code {
	case consent.FatalUnreachable:
		s = f.URL + " didn't answer: " + f.Detail
	case consent.FatalHTTPStatus:
		s = fmt.Sprintf("%s answered %d %s", f.URL, f.Status, http.StatusText(f.Status))
	case consent.FatalBotWall:
		s = f.URL + " showed a bot check (" + f.Detail + ") instead of the page"
	case consent.FatalRedirectHost:
		s = f.URL + " redirects to " + f.To
	case consent.FatalTLS:
		s = f.URL + " uses a certificate this machine doesn't trust (" + f.Detail + ")"
	case consent.FatalRedirectLoop:
		s = f.URL + ": " + f.Detail
	case consent.FatalSelector:
		s = f.Detail
	case consent.FatalRefused:
		s = consent.RefusedText(f)
	default:
		s = strings.TrimPrefix(f.Error(), "consent: ")
	}
	return consent.SafeText(s, 300)
}

// catalogInfo is the tracker catalog as a result names it. Info counts
// the built-in entries; the result counts what the check matched
// against, the config's own trackers included.
func catalogInfo(cl consent.Classifier) consent.DataInfo {
	info := cl.Info()
	info.Count = len(cl.Trackers())
	return info
}

// result is the check's setup: the browser, the user agent and the
// settings every writer prints, with the network counts so far.
func (c *check) result(started time.Time) *consent.Result {
	p, e := c.plan, c.e
	viewport := strings.ToLower(strings.TrimSpace(p.Viewport))
	if viewport == "" {
		viewport = ViewportDesktop
	}
	res := &consent.Result{
		Started:      started.UTC(),
		Echo:         p.Version,
		Browser:      e.Browser(),
		UserAgent:    newEmulation(p, consent.RunNone, e.ua, e.b.Version).ua,
		OS:           runtime.GOOS + "/" + runtime.GOARCH,
		Sandbox:      !e.b.NoSandbox,
		TLSVerified:  !p.Insecure && !e.PlainMapped(),
		Held:         p.Hold,
		Mapped:       e.Mapped(),
		Geo:          p.Geo,
		Locale:       planLocale(p),
		Timezone:     planTimezone(p),
		Viewport:     viewport,
		Pages:        []consent.Page{},
		Issues:       []consent.Issue{},
		Inconclusive: []consent.Inconclusive{},
		Notes:        []string{},
		ShowIDs:      p.ShowIDs,
	}
	if cl := c.stages.Classifier; cl != nil {
		res.Catalog = catalogInfo(cl)
	}
	if cm := c.stages.CMP; cm != nil {
		res.CMPRules = cm.Info()
	}
	res.Network = e.Network()
	c.mu.Lock()
	res.Network.Requests, res.Network.Held = c.requests, c.held
	res.Notes = append(res.Notes, c.notes...)
	if e.PlainMapped() && !slices.Contains(res.Notes, NoteMappedHTTP) {
		res.Notes = append(res.Notes, NoteMappedHTTP)
	}
	c.mu.Unlock()
	return res
}
