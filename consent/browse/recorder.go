// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/rhythm/kit/httpx"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/browse/egress"
)

// targetKind says what an attached target is.
type targetKind int

const (
	kindPage   targetKind = iota // the run's tab
	kindFrame                    // an out-of-process iframe
	kindWorker                   // a dedicated, shared or service worker
	kindPopup                    // a page the tab opened, closed at once
)

// Target types rod's version of the protocol has no constant for.
const (
	typeIframe proto.TargetTargetInfoType = "iframe"
	typeWorker proto.TargetTargetInfoType = "worker"
)

// target is one attached page, frame or worker of a run.
type target struct {
	s      cdp
	kind   targetKind
	id     proto.TargetTargetID
	origin string // a worker's origin
	// fetched is closed once the target's Fetch owner is in place.
	fetched     chan struct{}
	fetchedOnce sync.Once
}

// boundary is a moment a run's phase changes: the choice (and for the
// withdraw run the second choice).
type boundary struct {
	at    int64
	phase consent.Phase
}

// pending is a request seen by Fetch, Network or both, until it is
// recorded.
type pending struct {
	req       *consent.Request
	hit       *consent.Hit
	paused    bool    // Fetch has seen and answered it
	answered  bool    // held, mapped or refused: recorded at once
	will      bool    // Network.requestWillBeSent has been seen
	wall      float64 // seconds since the epoch, from requestWillBeSent
	pausedAt  float64 // wall seconds when Fetch paused it, for requests Network never reported
	doc       string  // requestWillBeSent documentURL
	initiator string
	worker    bool
	// workerOrigin is the origin of the worker that sent the request.
	workerOrigin string
	frameless    bool // no frame and no worker: browser traffic
	recorded     bool
}

// heldGIF is a transparent 1x1 GIF, 43 bytes: what a held image request
// gets, so a page that checks the pixel loaded sees it load.
var heldGIF = []byte{
	0x47, 0x49, 0x46, 0x38, 0x39, 0x61, 0x01, 0x00, 0x01, 0x00, 0x80, 0x00, 0x00, 0x00, 0x00, 0x00,
	0xff, 0xff, 0xff, 0x21, 0xf9, 0x04, 0x01, 0x00, 0x00, 0x00, 0x00, 0x2c, 0x00, 0x00, 0x00, 0x00,
	0x01, 0x00, 0x01, 0x00, 0x00, 0x02, 0x02, 0x44, 0x01, 0x00, 0x3b,
}

// maxHandlers bounds the requests a recorder works on at once. Each
// answers its request itself, so a slow body read never holds up the
// next request.
const maxHandlers = 64

// recorder turns one run's DevTools events into consent events: one
// Fetch owner on every target of the run's context (the tab, its
// out-of-process frames and its workers), each request recorded,
// classified and held, continued or answered, all on one clock.
//
// Events reach the run through a bounded channel. The Fetch handler
// never waits on it: a full channel drops the event, counts it and marks
// the run truncated. A writer goroutine numbers the events, gives each
// its phase and visit, keeps them and hands them to the sinks.
type recorder struct {
	h      *hub
	plan   *consent.Plan
	site   *url.URL
	kind   consent.RunKind
	ctxID  proto.BrowserBrowserContextID
	stages consent.Stages
	env    *emulation
	guard  *egress.Proxy // the target's guard, for refusal reasons; may be nil
	mapper *mapper
	ctx    context.Context

	ready     chan struct{} // the tab is attached, set up and running
	readyOnce sync.Once
	readyErr  error

	inbox chan *rod.Message
	stop  chan struct{}
	loopD chan struct{}
	sem   chan struct{}
	work  sync.WaitGroup

	mu         sync.Mutex
	targets    map[proto.TargetSessionID]*target
	main       *target
	frames     map[proto.PageFrameID]string // frame id -> origin
	reqs       map[proto.NetworkRequestID]*pending
	base       float64 // wall seconds of the first navigation request; 0 until then
	fallback   float64 // wall seconds when the run asked to navigate
	boundaries []boundary
	visits     []int64
	choiceWait bool
	nRequests  int
	limitHit   bool
	hookN      int // hook records read
	hookBytes  int

	clicks chan int64 // pointerdown times from hooks.js, newest last

	limit    chan struct{} // closed when Limits.Requests is reached
	counts   struct{ requests, held, refused, internal atomic.Int64 }
	dropped  atomic.Int64
	trunc    atomic.Bool
	closedIn atomic.Bool

	events   chan consent.Event
	wake     chan struct{}
	writerD  chan struct{}
	seq      int64
	out      []consent.Event
	sinks    []func(consent.Event)
	finished bool
}

// newRecorder makes the recorder for one run. env is the emulation the
// session applies to every target; guard is the target's egress guard.
func newRecorder(ctx context.Context, h *hub, p *consent.Plan, site *url.URL, kind consent.RunKind,
	ctxID proto.BrowserBrowserContextID, env *emulation, guard *egress.Proxy) *recorder {
	ch := p.Limits.Channel
	if ch <= 0 {
		ch = consent.DefaultLimits.Channel
	}
	r := &recorder{h: h, plan: p, site: site, kind: kind, ctxID: ctxID, stages: consent.Linked(), env: env, guard: guard,
		mapper: newMapper(p.MapHosts), ctx: ctx,
		ready: make(chan struct{}), inbox: make(chan *rod.Message, 16384), stop: make(chan struct{}), loopD: make(chan struct{}),
		sem: make(chan struct{}, maxHandlers), targets: map[proto.TargetSessionID]*target{}, frames: map[proto.PageFrameID]string{},
		reqs: map[proto.NetworkRequestID]*pending{}, limit: make(chan struct{}), clicks: make(chan int64, 16),
		events: make(chan consent.Event, ch), wake: make(chan struct{}, 1), writerD: make(chan struct{})}
	if p.Live != nil {
		live := p.Live
		r.sinks = append(r.sinks, func(e consent.Event) {
			if e.Kind == consent.EvRequest && e.Hit != nil {
				live(e)
			}
		})
	}
	go r.loop()
	go r.writer()
	return r
}

// limits are the plan's limits with defaults for the zero ones.
func (r *recorder) limits() consent.Limits {
	l := r.plan.Limits
	d := consent.DefaultLimits
	if l.Requests <= 0 {
		l.Requests = d.Requests
	}
	if l.URLBytes <= 0 {
		l.URLBytes = d.URLBytes
	}
	if l.BodyBytes <= 0 {
		l.BodyBytes = d.BodyBytes
	}
	if l.HookRecords <= 0 {
		l.HookRecords = d.HookRecords
	}
	if l.HookBytes <= 0 {
		l.HookBytes = d.HookBytes
	}
	if l.Screenshots <= 0 {
		l.Screenshots = d.Screenshots
	}
	return l
}

// waitReady waits until the tab is set up and running.
func (r *recorder) waitReady(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-r.ready:
		return r.readyErr
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return errors.New("consent: Chrome didn't attach the run's tab")
	}
}

// attached handles a new target in the run's context (parent "" for a
// browser-level one). The first page is the run's tab; any later page is
// a popup, recorded and closed before it loads. Frames and workers get a
// Fetch owner before they resume.
func (r *recorder) attached(parent proto.TargetSessionID, e proto.TargetAttachedToTarget) {
	info := e.TargetInfo
	t := &target{s: cdp{b: r.h.root.b, id: e.SessionID, ctx: r.ctx}, id: info.TargetID, fetched: make(chan struct{})}
	switch info.Type {
	case proto.TargetTargetInfoTypePage:
		r.mu.Lock()
		if r.main != nil && r.main.id == info.TargetID {
			// Another session to the tab (rod's own): not ours to set up.
			r.mu.Unlock()
			r.h.disown(e.SessionID)
			return
		}
		isMain := parent == "" && r.main == nil
		if isMain {
			t.kind = kindPage
			r.main = t
			r.frames[proto.PageFrameID(info.TargetID)] = origin(info.URL)
		}
		r.mu.Unlock()
		if !isMain {
			r.popup(t, e)
			return
		}
	case typeIframe:
		t.kind = kindFrame
	case typeWorker, proto.TargetTargetInfoTypeSharedWorker, proto.TargetTargetInfoTypeServiceWorker:
		t.kind = kindWorker
		t.origin = origin(info.URL)
	default:
		// Anything else (a worklet, an auction) is let go as it is.
		go func() {
			if e.WaitingForDebugger {
				_ = proto.RuntimeRunIfWaitingForDebugger{}.Call(t.s)
			}
		}()
		return
	}
	r.mu.Lock()
	for _, other := range r.targets {
		if other.id == t.id {
			// The same worker attached a second time (under the page and
			// at the browser level): the first session owns it. Either
			// session's resume starts the worker, so this one lets go only
			// once the first has its Fetch owner in place.
			r.mu.Unlock()
			r.h.disown(e.SessionID)
			if e.WaitingForDebugger {
				go func() {
					<-other.fetched
					_ = proto.RuntimeRunIfWaitingForDebugger{}.Call(t.s)
				}()
			}
			return
		}
	}
	r.targets[e.SessionID] = t
	r.mu.Unlock()
	go func() {
		defer t.fetchedOnce.Do(func() { close(t.fetched) })
		err := r.setup(t)
		if e.WaitingForDebugger {
			if rerr := (proto.RuntimeRunIfWaitingForDebugger{}).Call(t.s); err == nil {
				err = rerr
			}
		}
		if t.kind == kindPage {
			r.readyOnce.Do(func() { r.readyErr = err; close(r.ready) })
		}
	}()
}

// popup records a page the run's tab opened and closes it. The opener
// may share its renderer, which a paused popup would hold up, so the
// popup is resumed first, with every request it makes failed, so it
// can't send anything before it is gone.
func (r *recorder) popup(t *target, e proto.TargetAttachedToTarget) {
	t.kind = kindPopup
	r.mu.Lock()
	r.targets[e.SessionID] = t
	r.mu.Unlock()
	r.emit(consent.Event{Kind: consent.EvPopup, AtMS: r.now(), Note: consent.SafeText(consent.MaskURL(e.TargetInfo.URL, r.plan.ShowIDs), 200)})
	go func() {
		_ = proto.FetchEnable{Patterns: []*proto.FetchRequestPattern{{URLPattern: "*", RequestStage: proto.FetchRequestStageRequest}}}.Call(t.s)
		if e.WaitingForDebugger {
			_ = proto.RuntimeRunIfWaitingForDebugger{}.Call(t.s)
		}
		_, _ = proto.TargetCloseTarget{TargetID: t.id}.Call(r.h.root)
	}()
}

// setup gives a target its Fetch owner, Network events, auto-attach for
// its own children and the run's emulation, all before it runs.
//
// Fetch comes first: Chrome answers it while the target is paused. A
// worker's Network domain lives on the worker's own thread and answers
// only once something resumed the worker.
func (r *recorder) setup(t *target) error {
	err := (proto.FetchEnable{Patterns: []*proto.FetchRequestPattern{{URLPattern: "*", RequestStage: proto.FetchRequestStageRequest}}}).Call(t.s)
	t.fetchedOnce.Do(func() { close(t.fetched) })
	if err != nil && t.kind != kindWorker {
		// A dedicated worker has no Fetch domain of its own: its
		// requests pause on the page's while its Network events mark
		// them as the worker's.
		return err
	}
	maxPost := r.limits().BodyBytes
	if err := (proto.NetworkEnable{MaxPostDataSize: &maxPost}).Call(t.s); err != nil {
		return err
	}
	if t.kind != kindWorker {
		if err := (proto.PageEnable{}).Call(t.s); err != nil {
			return err
		}
		if err := r.installHooks(t); err != nil {
			return err
		}
	}
	// A worker may refuse auto-attach; its own children are then only
	// seen through the page.
	if err := (proto.TargetSetAutoAttach{AutoAttach: true, WaitForDebuggerOnStart: true, Flatten: true}).Call(t.s); err != nil && t.kind != kindWorker {
		return err
	}
	if r.env != nil {
		return r.env.apply(t)
	}
	return nil
}

// detached forgets a target that went away.
func (r *recorder) detached(s proto.TargetSessionID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.targets, s)
}

// event queues one event of the run's sessions for the loop. It waits
// when the queue is full rather than lose a paused request, which would
// hang the page; after close it drops everything.
func (r *recorder) event(msg *rod.Message) {
	if r.closedIn.Load() {
		return
	}
	select {
	case r.inbox <- msg:
	case <-r.stop:
	}
}

// loop handles queued events in order. Paused requests are handled on
// their own goroutines, bounded by sem, so the loop never waits on
// Chrome.
func (r *recorder) loop() {
	defer close(r.loopD)
	for {
		select {
		case <-r.stop:
			return
		case msg := <-r.inbox:
			r.handle(msg)
		}
	}
}

func (r *recorder) handle(msg *rod.Message) {
	defer func() { _ = recover() }() // rod's Load panics on an event it can't decode
	r.mu.Lock()
	t := r.targets[msg.SessionID]
	r.mu.Unlock()
	if t == nil {
		return
	}
	switch msg.Method {
	case "Fetch.requestPaused":
		var e proto.FetchRequestPaused
		if msg.Load(&e) && t.kind == kindPopup {
			go func() {
				_ = proto.FetchFailRequest{RequestID: e.RequestID, ErrorReason: proto.NetworkErrorReasonBlockedByClient}.Call(t.s)
			}()
			return
		}
		if e.Request != nil {
			r.sem <- struct{}{}
			r.work.Add(1)
			go func() {
				defer func() { <-r.sem; r.work.Done() }()
				r.paused(t, &e)
			}()
		}
	case "Network.requestWillBeSent":
		var e proto.NetworkRequestWillBeSent
		if msg.Load(&e) {
			r.willBeSent(t, &e)
		}
	case "Network.responseReceived":
		var e proto.NetworkResponseReceived
		if msg.Load(&e) && e.Response != nil {
			r.completed(e.RequestID, e.Response.Status, header(e.Response.Headers, "X-Rhythm-Echo-Refused"))
		}
	case "Network.loadingFailed":
		var e proto.NetworkLoadingFailed
		if msg.Load(&e) {
			r.completed(e.RequestID, 0, e.ErrorText)
		}
	case "Network.loadingFinished":
		var e proto.NetworkLoadingFinished
		if msg.Load(&e) {
			r.completed(e.RequestID, -1, "")
		}
	case "Page.frameNavigated":
		var e proto.PageFrameNavigated
		if msg.Load(&e) && e.Frame != nil {
			r.mu.Lock()
			r.frames[e.Frame.ID] = origin(e.Frame.URL)
			r.mu.Unlock()
		}
	case "Page.frameAttached":
		var e proto.PageFrameAttached
		if msg.Load(&e) {
			r.mu.Lock()
			if _, ok := r.frames[e.FrameID]; !ok {
				r.frames[e.FrameID] = ""
			}
			r.mu.Unlock()
		}
	case "Page.javascriptDialogOpening":
		var e proto.PageJavascriptDialogOpening
		if msg.Load(&e) {
			r.emit(consent.Event{Kind: consent.EvDialog, AtMS: r.now(), Note: consent.SafeText(e.Message, 200)})
			go func() { _ = proto.PageHandleJavaScriptDialog{Accept: false}.Call(t.s) }()
		}
	case "Runtime.bindingCalled":
		var e proto.RuntimeBindingCalled
		if msg.Load(&e) && e.Name == bindingName {
			r.hook(e.Payload)
		}
	case "Page.fileChooserOpened":
		var e proto.PageFileChooserOpened
		if msg.Load(&e) {
			r.emit(consent.Event{Kind: consent.EvNote, AtMS: r.now(), Note: "the page opened a file chooser; Echo canceled it"})
		}
	}
}

// download records a download Chrome refused, when it came from one of
// the run's frames.
func (r *recorder) download(e proto.BrowserDownloadWillBegin) {
	r.mu.Lock()
	_, ours := r.frames[e.FrameID]
	r.mu.Unlock()
	if ours {
		r.emit(consent.Event{Kind: consent.EvDownload, AtMS: r.now(), Note: consent.SafeText(consent.MaskURL(e.URL, r.plan.ShowIDs), 200)})
	}
}

// pendingFor returns the pending entry for id, making one.
func (r *recorder) pendingFor(id proto.NetworkRequestID) *pending {
	p := r.reqs[id]
	if p == nil {
		p = &pending{}
		r.reqs[id] = p
	}
	return p
}

// willBeSent notes a request's wall time, initiator and document. The
// first document request of the tab starts the run clock. A redirect
// ends the hop before it.
func (r *recorder) willBeSent(t *target, e *proto.NetworkRequestWillBeSent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.base == 0 && t.kind == kindPage && e.Type == proto.NetworkResourceTypeDocument {
		r.base = float64(e.WallTime)
	}
	p := r.reqs[e.RequestID]
	if e.RedirectResponse != nil && p != nil && p.paused {
		// The same id carries the next hop: record this one as it ended.
		p.req.Status = e.RedirectResponse.Status
		r.recordLocked(p)
		delete(r.reqs, e.RequestID)
		p = nil
	}
	if p == nil {
		p = r.pendingFor(e.RequestID)
	}
	if t.kind == kindWorker {
		// A worker's own session reports the request too: it is the
		// worker's, whichever session paused it.
		p.worker, p.workerOrigin = true, t.origin
	}
	if p.will {
		return
	}
	p.will, p.wall, p.doc = true, float64(e.WallTime), e.DocumentURL
	p.initiator = initiatorURL(e.Initiator)
	if p.paused {
		r.finishPendingLocked(p)
	}
	if len(r.reqs) > 4*r.limits().Requests {
		// Network-only requests (memory cache, data: URLs) never reach
		// Fetch; forget the oldest so the table stays bounded.
		for id, q := range r.reqs {
			if !q.paused {
				delete(r.reqs, id)
				break
			}
		}
	}
}

// completed records a continued request once it has a status or failed.
// status -1 means finished without a new status; errText is Chrome's
// error, or the guard's refusal header on a response.
func (r *recorder) completed(id proto.NetworkRequestID, status int, errText string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.reqs[id]
	if p == nil {
		return
	}
	if !p.paused {
		if status != 0 || errText != "" {
			delete(r.reqs, id) // never intercepted: not recorded
		}
		return
	}
	if status > 0 {
		// A plain-HTTP request the guard refused gets its 403 as the
		// response; the guard's own record says it was the guard, so a
		// site can't mark its requests refused with the header alone.
		p.req.Status = status
		if errText != "" && status == 403 {
			r.refusedLocked(p)
		}
		return // loadingFinished or loadingFailed comes next
	}
	if errText != "" {
		r.refusedLocked(p)
	}
	r.recordLocked(p)
	delete(r.reqs, id)
}

// refusedLocked marks a request the guard refused with the guard's
// reason.
func (r *recorder) refusedLocked(p *pending) {
	if p.req.Refused != "" || r.guard == nil {
		return
	}
	if reason := r.guard.Reason(p.req.Host, urlPort(p.req)); reason != "" {
		p.req.Refused = reason
	}
}

// finishPendingLocked records a request answered at once now that its
// time is known.
func (r *recorder) finishPendingLocked(p *pending) {
	if p.answered {
		r.recordLocked(p)
	}
}

// recordLocked turns a pending request into its event, once.
func (r *recorder) recordLocked(p *pending) {
	if p.recorded || p.req == nil {
		return
	}
	p.recorded = true
	if p.frameless {
		return // browser traffic: counted, never classified or recorded
	}
	r.fillLocked(p)
	r.emit(consent.Event{Kind: consent.EvRequest, AtMS: r.requestAtLocked(p), Request: p.req, Hit: p.hit})
}

// requestAtLocked is a request's time on the run clock: Network's wall
// time, else when Fetch paused it.
func (r *recorder) requestAtLocked(p *pending) int64 {
	switch {
	case p.will:
		return r.atWallLocked(p.wall)
	case p.pausedAt != 0:
		return r.atWallLocked(p.pausedAt)
	}
	return r.nowLocked()
}

// fillLocked completes a request from what Network said about it: the
// worker that sent it, its frame's origin and its initiator.
func (r *recorder) fillLocked(p *pending) {
	if p.worker {
		p.req.Worker = true
		if p.workerOrigin != "" {
			p.req.Frame = p.workerOrigin
		}
	}
	if p.req.Frame == "" && p.doc != "" {
		p.req.Frame = origin(p.doc)
	}
	if p.req.Initiator == "" && p.initiator != "" {
		p.req.Initiator = r.cut(consent.MaskURL(p.initiator, r.plan.ShowIDs))
	}
}

// paused handles one Fetch.requestPaused: build the request, read its
// body, classify it and answer it. It runs on its own goroutine.
func (r *recorder) paused(t *target, e *proto.FetchRequestPaused) {
	if e.Request == nil {
		return
	}
	pausedAt := wallNow()
	ans := cdp{b: t.s.b, id: t.s.id, ctx: context.WithoutCancel(r.ctx)}
	raw := e.Request.URL + e.Request.URLFragment
	u, err := url.Parse(e.Request.URL)
	if err != nil || u.Host == "" {
		_ = proto.FetchContinueRequest{RequestID: e.RequestID}.Call(ans)
		return
	}
	worker := t.kind == kindWorker
	r.mu.Lock()
	frame, known := r.frames[e.FrameID]
	frameless := e.FrameID == "" && !worker
	if worker {
		frame = t.origin
	}
	limited := false
	if !frameless {
		r.nRequests++
		if r.nRequests > r.limits().Requests {
			limited = true
			if !r.limitHit {
				r.limitHit = true
				close(r.limit)
			}
		}
	}
	r.mu.Unlock()
	if frameless {
		// Chrome's own request from inside the context (no page frame,
		// no worker of the page): never classified, so it can't be a hit.
		r.counts.internal.Add(1)
		r.markFrameless(e.NetworkID)
		_ = proto.FetchContinueRequest{RequestID: e.RequestID}.Call(ans)
		return
	}
	if limited {
		// Past the limit nothing more is recorded; failing the request
		// keeps an unrecorded hit from leaving.
		r.trunc.Store(true)
		r.dropped.Add(1)
		_ = proto.FetchFailRequest{RequestID: e.RequestID, ErrorReason: proto.NetworkErrorReasonBlockedByClient}.Call(ans)
		return
	}
	if !known && !worker {
		frame = ""
	}
	req := &consent.Request{
		Method: e.Request.Method,
		URL:    r.cut(consent.MaskURL(raw, r.plan.ShowIDs)),
		Host:   strings.ToLower(u.Hostname()),
		Type:   resourceType(e.ResourceType),
		Frame:  frame,
		Worker: worker,
	}
	body, partial := r.body(ans, e)
	window := body
	if lim := r.limits().BodyBytes; len(window) > lim {
		window, partial = window[:lim], true
	}
	if len(body) > 0 {
		sum := sha256.Sum256(body)
		req.BodyBytes, req.BodySHA256 = len(body), hex.EncodeToString(sum[:])
	}
	req.BodyPartial = partial
	r.counts.requests.Add(1)

	var hit *consent.Hit
	preflight := req.Method == "OPTIONS"
	if c := r.stages.Classifier; c != nil && !preflight {
		hit = safeClassify(c, req, window, r.site)
		if hit != nil {
			hit = r.decodeGoogle(hit)
		}
	}
	hold := r.plan.Hold && r.stages.Classifier != nil && safeHold(r.stages.Classifier, req)
	mapped, mapAddr := r.mapper.lookup(req.Host)

	answered := true
	switch {
	case hold:
		req.Held = true
		r.counts.held.Add(1)
		r.fulfillHeld(ans, e, req)
	case mapped:
		req.Mapped = true
		req.Status = r.fetchMapped(ans, e, u, body, mapAddr)
	case r.plan.MapOnly:
		req.Refused = egress.ReasonNotMapped
		r.counts.refused.Add(1)
		_ = proto.FetchFailRequest{RequestID: e.RequestID, ErrorReason: proto.NetworkErrorReasonBlockedByClient}.Call(ans)
	default:
		answered = false
		_ = proto.FetchContinueRequest{RequestID: e.RequestID, Headers: r.sameSiteHeaders(u, e.Request.Headers)}.Call(ans)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if e.NetworkID == "" {
		p := &pending{req: req, hit: hit, paused: true, answered: true, pausedAt: pausedAt}
		r.recordLocked(p)
		return
	}
	p := r.pendingFor(e.NetworkID)
	if p.paused {
		// A redirect hop Chrome sent without a requestWillBeSent for the
		// one before: record that hop as it was.
		r.recordLocked(p)
		p = &pending{will: p.will, wall: p.wall, doc: p.doc, initiator: p.initiator}
		r.reqs[e.NetworkID] = p
	}
	p.req, p.hit, p.paused, p.answered, p.pausedAt = req, hit, true, answered, pausedAt
	p.worker = p.worker || worker
	if p.will {
		r.finishPendingLocked(p)
		return
	}
	if answered {
		// Network may never report it (a worker whose Network domain
		// came up after its first request): record it on its pause time
		// soon rather than at the end of the run.
		id := e.NetworkID
		time.AfterFunc(willWait, func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			if !p.recorded {
				r.recordLocked(p)
				if r.reqs[id] == p {
					delete(r.reqs, id)
				}
			}
		})
	}
}

// willWait is how long an answered request waits for Network's report
// of it before it is recorded without one.
var willWait = 500 * time.Millisecond

// markFrameless notes that a network id is browser traffic.
func (r *recorder) markFrameless(id proto.NetworkRequestID) {
	if id == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.pendingFor(id)
	p.paused, p.frameless, p.recorded = true, true, true
}

// body reads a request's body: Fetch's postData or its entries, else
// Network.getRequestPostData when Chrome left a body out. partial is set
// when the body exists but couldn't be read.
func (r *recorder) body(s cdp, e *proto.FetchRequestPaused) ([]byte, bool) {
	req := e.Request
	if len(req.PostDataEntries) > 0 {
		var b []byte
		for _, en := range req.PostDataEntries {
			b = append(b, en.Bytes...)
		}
		return b, false
	}
	if req.PostData != "" {
		return []byte(req.PostData), false
	}
	if !req.HasPostData {
		return nil, false
	}
	if e.NetworkID == "" {
		return nil, true
	}
	res, err := proto.NetworkGetRequestPostData{RequestID: e.NetworkID}.Call(s)
	if err != nil || res == nil {
		return nil, true
	}
	return []byte(res.PostData), false
}

// fulfillHeld answers a held hit in the browser: 204 with no body, or
// the transparent GIF for an image. CORS fetches get the headers they
// need, so the page sees the same success it would from the tracker.
func (r *recorder) fulfillHeld(s cdp, e *proto.FetchRequestPaused, req *consent.Request) {
	headers := []*proto.FetchHeaderEntry{{Name: "Cache-Control", Value: "no-store"}}
	if o := header(e.Request.Headers, "Origin"); o != "" {
		headers = append(headers, &proto.FetchHeaderEntry{Name: "Access-Control-Allow-Origin", Value: o},
			&proto.FetchHeaderEntry{Name: "Access-Control-Allow-Credentials", Value: "true"},
			&proto.FetchHeaderEntry{Name: "Access-Control-Allow-Headers", Value: "*"},
			&proto.FetchHeaderEntry{Name: "Access-Control-Allow-Methods", Value: "GET, POST, OPTIONS"})
	}
	code, body := 204, []byte{}
	if req.Type == "image" {
		code, body = 200, heldGIF
		headers = append(headers, &proto.FetchHeaderEntry{Name: "Content-Type", Value: "image/gif"})
	}
	req.Status = code
	_ = proto.FetchFulfillRequest{RequestID: e.RequestID, ResponseCode: code, ResponseHeaders: headers, Body: body}.Call(s)
}

// sameSiteHeaders adds the plan's -H headers to a same-site request.
// Other requests keep Chrome's headers (nil means unchanged).
func (r *recorder) sameSiteHeaders(u *url.URL, have proto.NetworkHeaders) []*proto.FetchHeaderEntry {
	if len(r.plan.Headers) == 0 || !httpx.SameSite(r.site, u) {
		return nil
	}
	out := make([]*proto.FetchHeaderEntry, 0, len(have)+len(r.plan.Headers))
	for name, v := range have {
		if _, ours := lookupFold(r.plan.Headers, name); ours {
			continue
		}
		out = append(out, &proto.FetchHeaderEntry{Name: name, Value: v.Str()})
	}
	for name, v := range r.plan.Headers {
		out = append(out, &proto.FetchHeaderEntry{Name: name, Value: v})
	}
	return out
}

// decodeGoogle fills the decoded gcs and gcd of a Google hit. The gcd
// reading is the community's, never Google's, so it is marked so.
func (r *recorder) decodeGoogle(h *consent.Hit) *consent.Hit {
	g := h.Google
	sig := r.stages.Signals
	if g == nil || sig == nil {
		return h
	}
	defer func() { _ = recover() }() // a decoder bug must not lose the hit
	if g.GCS != "" && g.GCSDecoded == nil {
		d := sig.GCS(g.GCS)
		g.GCSDecoded = &d
	}
	if g.GCD != "" && g.GCDDecoded == nil {
		d := sig.GCD(g.GCD)
		g.GCDDecoded = &d
		g.GCDUnofficial = true
	}
	return h
}

// safeClassify calls the catalog, which must never panic; if it does,
// the request is simply not a hit rather than a crashed run.
func safeClassify(c consent.Classifier, req *consent.Request, body []byte, site *url.URL) (h *consent.Hit) {
	defer func() {
		if recover() != nil {
			h = nil
		}
	}()
	return c.Request(req, body, site)
}

func safeHold(c consent.Classifier, req *consent.Request) (hold bool) {
	defer func() {
		if recover() != nil {
			hold = false
		}
	}()
	return c.Hold(req)
}

// emit hands an event to the writer without waiting: a full channel
// drops it, counts it and marks the run truncated.
func (r *recorder) emit(e consent.Event) {
	if r.closedIn.Load() {
		return
	}
	select {
	case r.events <- e:
	default:
		r.dropped.Add(1)
		r.trunc.Store(true)
	}
}

// writer numbers, phases and keeps the events in order of arrival.
// While a choice is being made (beginChoice until setChoice) it holds
// them back, so an event from the click's own task isn't written as
// before the choice whose time isn't known yet.
func (r *recorder) writer() {
	defer close(r.writerD)
	var held []consent.Event
	flush := func() {
		for _, e := range held {
			r.deliver(e)
		}
		held = held[:0]
	}
	for {
		select {
		case e, ok := <-r.events:
			if !ok {
				flush()
				return
			}
			r.mu.Lock()
			waiting := r.choiceWait
			r.mu.Unlock()
			if waiting {
				if len(held) >= cap(r.events) {
					r.dropped.Add(1)
					r.trunc.Store(true)
					continue
				}
				held = append(held, e)
				continue
			}
			flush()
			r.deliver(e)
		case <-r.wake:
			r.mu.Lock()
			waiting := r.choiceWait
			r.mu.Unlock()
			if !waiting {
				flush()
			}
		}
	}
}

// deliver gives an event its number, phase and visit, keeps it and
// hands it to the sinks.
func (r *recorder) deliver(e consent.Event) {
	r.seq++
	e.Seq = r.seq
	r.mu.Lock()
	e.Phase = r.phaseLocked(e.AtMS)
	e.Visit = r.visitLocked(e.AtMS)
	if e.Cookie != nil {
		e.Cookie.Phase, e.Cookie.AtMS = e.Phase, e.AtMS
	}
	if e.Storage != nil {
		e.Storage.Phase, e.Storage.AtMS = e.Phase, e.AtMS
	}
	if e.Signal != nil {
		e.Signal.AtMS = e.AtMS
	}
	r.out = append(r.out, e)
	r.mu.Unlock()
	for _, s := range r.sinks {
		s(e)
	}
}

// beginChoice is called just before the run clicks a choice.
func (r *recorder) beginChoice() {
	r.mu.Lock()
	r.choiceWait = true
	r.mu.Unlock()
}

// setChoice records that from at on the run is in phase ph and lets the
// held events go. at < 0 means no choice was made.
func (r *recorder) setChoice(at int64, ph consent.Phase) {
	r.mu.Lock()
	if at >= 0 {
		r.boundaries = append(r.boundaries, boundary{at, ph})
	}
	r.choiceWait = false
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// addVisit starts a new visit at at.
func (r *recorder) addVisit(at int64) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.visits = append(r.visits, at)
	return len(r.visits) - 1
}

// startPhase is the phase a run is in before any choice.
func (r *recorder) startPhase() consent.Phase {
	if r.kind == consent.RunGPC {
		return consent.PhaseGPC
	}
	return consent.PhaseBefore
}

// phaseLocked is the phase at run time at: the last boundary at or
// before it. A request at the click's own millisecond is after it.
func (r *recorder) phaseLocked(at int64) consent.Phase {
	ph := r.startPhase()
	for _, b := range r.boundaries {
		if at >= b.at {
			ph = b.phase
		}
	}
	return ph
}

func (r *recorder) visitLocked(at int64) int {
	v := 0
	for i, start := range r.visits {
		if at >= start {
			v = i
		}
	}
	return v
}

// navigating notes the wall time the run asked for its first page, the
// clock's start if Chrome reports no document request.
func (r *recorder) navigating() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fallback == 0 {
		r.fallback = wallNow()
	}
}

// atWall is a wall time (seconds) on the run clock, in whole ms.
func (r *recorder) atWallLocked(wall float64) int64 {
	base := r.base
	if base == 0 {
		base = r.fallback
	}
	if base == 0 {
		base = wall
		r.fallback = wall
	}
	return int64(math.Floor((wall - base) * 1000))
}

// atPage is a page time (performance.timeOrigin + performance.now(),
// ms) on the run clock.
func (r *recorder) atPage(ms float64) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.atWallLocked(ms / 1000)
}

// now is the current time on the run clock.
func (r *recorder) now() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.nowLocked()
}

func (r *recorder) nowLocked() int64 { return r.atWallLocked(wallNow()) }

// wallNow is the wall clock in seconds since the epoch, as Chrome's
// wallTime counts it.
func wallNow() float64 { return float64(time.Now().UnixMicro()) / 1e6 }

// finish stops recording: requests still waiting for their end are
// recorded as they are, the writer drains and every event's phase and
// visit are worked out again from the final choice times.
func (r *recorder) finish() *Recording {
	r.mu.Lock()
	if r.finished {
		r.mu.Unlock()
		return r.result()
	}
	r.finished = true
	r.mu.Unlock()
	r.closedIn.Store(true)
	close(r.stop)
	<-r.loopD
	r.work.Wait()
	r.mu.Lock()
	r.choiceWait = false
	for _, p := range r.reqs {
		if p.paused {
			r.recordLockedFinal(p)
		}
	}
	r.reqs = map[proto.NetworkRequestID]*pending{}
	r.mu.Unlock()
	close(r.events)
	<-r.writerD
	r.mu.Lock()
	for i := range r.out {
		e := &r.out[i]
		e.Phase = r.phaseLocked(e.AtMS)
		e.Visit = r.visitLocked(e.AtMS)
		if e.Cookie != nil {
			e.Cookie.Phase = e.Phase
		}
		if e.Storage != nil {
			e.Storage.Phase = e.Phase
		}
	}
	r.mu.Unlock()
	return r.result()
}

// recordLockedFinal is recordLocked for the last flush, after emit is
// closed to new events from handlers: it sends straight to the channel.
func (r *recorder) recordLockedFinal(p *pending) {
	if p.recorded || p.req == nil || p.frameless {
		return
	}
	p.recorded = true
	r.fillLocked(p)
	select {
	case r.events <- consent.Event{Kind: consent.EvRequest, AtMS: r.requestAtLocked(p), Request: p.req, Hit: p.hit}:
	default:
		r.dropped.Add(1)
		r.trunc.Store(true)
	}
}

func (r *recorder) result() *Recording {
	r.mu.Lock()
	defer r.mu.Unlock()
	return &Recording{Events: r.out, Dropped: int(r.dropped.Load()), Truncated: r.trunc.Load(), Limit: r.limitHit,
		Requests: int(r.counts.requests.Load()), Held: int(r.counts.held.Load()), Refused: int(r.counts.refused.Load()),
		Internal: int(r.counts.internal.Load())}
}

// cut caps a stored URL at Limits.URLBytes, on a rune boundary.
func (r *recorder) cut(s string) string {
	lim := r.limits().URLBytes
	if len(s) <= lim {
		return s
	}
	s = s[:lim]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// resourceType maps Chrome's resource type to the PRD's words.
// sendBeacon (and <a ping>) is "Ping" in Chrome; it is recorded as
// beacon.
func resourceType(t proto.NetworkResourceType) string {
	switch t {
	case proto.NetworkResourceTypeDocument:
		return "document"
	case proto.NetworkResourceTypeScript:
		return "script"
	case proto.NetworkResourceTypeImage:
		return "image"
	case proto.NetworkResourceTypeXHR:
		return "xhr"
	case proto.NetworkResourceTypeFetch:
		return "fetch"
	case proto.NetworkResourceTypePing:
		return "beacon"
	case proto.NetworkResourceTypeStylesheet:
		return "stylesheet"
	case proto.NetworkResourceTypeFont:
		return "font"
	case proto.NetworkResourceTypeMedia:
		return "media"
	}
	return "other"
}

// initiatorURL is the script that started a request: the initiator's
// URL, else the first frame of its stack (and the async parents) that
// has one.
func initiatorURL(in *proto.NetworkInitiator) string {
	if in == nil {
		return ""
	}
	if in.URL != "" {
		return in.URL
	}
	for st := in.Stack; st != nil; st = st.Parent {
		for _, f := range st.CallFrames {
			if f != nil && f.URL != "" {
				return f.URL
			}
		}
	}
	return ""
}

// origin is scheme://host[:port] of raw, or "" for a URL without one
// (about:blank).
func origin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return strings.ToLower(u.Scheme + "://" + u.Host)
}

// urlPort is the port a request's URL goes to, with the scheme's
// default.
func urlPort(req *consent.Request) string {
	u, err := url.Parse(req.URL)
	if err != nil {
		return ""
	}
	if p := u.Port(); p != "" {
		return p
	}
	if u.Scheme == "http" {
		return "80"
	}
	return "443"
}

// header reads a header from Chrome's map, ignoring case.
func header(h proto.NetworkHeaders, name string) string {
	for k, v := range h {
		if strings.EqualFold(k, name) {
			return v.Str()
		}
	}
	return ""
}

// lookupFold finds name in m, ignoring case.
func lookupFold(m map[string]string, name string) (string, bool) {
	for k, v := range m {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return "", false
}
