// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browse

import (
	"context"
	"fmt"
	"sync"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/echo/internal/browser"
)

// cdp is one DevTools session on the browser connection. It implements
// proto.Client, so proto requests are sent to that session.
type cdp struct {
	b   *rod.Browser
	id  proto.TargetSessionID
	ctx context.Context
}

// Call sends a raw command on the session.
func (c cdp) Call(ctx context.Context, _ string, method string, params any) ([]byte, error) {
	return c.b.Call(ctx, string(c.id), method, params)
}

// GetSessionID names the session the proto helpers send to.
func (c cdp) GetSessionID() proto.TargetSessionID { return c.id }

// GetContext bounds every command on the session.
func (c cdp) GetContext() context.Context { return c.ctx }

// hub is the one reader of a browser's DevTools events. It attaches to
// every new page and worker paused before it runs (browser-level
// auto-attach), hands each to the run whose context it belongs to and
// passes every later event of that session (and of the frames and
// workers under it) to the same run. A target in a context no run owns
// is let go at once.
type hub struct {
	b      *browser.Browser
	root   cdp
	cancel context.CancelFunc
	done   chan struct{}

	mu       sync.Mutex
	contexts map[proto.BrowserBrowserContextID]*recorder
	sessions map[proto.TargetSessionID]*recorder
}

// newHub starts routing b's events.
func newHub(ctx context.Context, b *browser.Browser) (*hub, error) {
	ctx, cancel := context.WithCancel(ctx)
	h := &hub{b: b, cancel: cancel, done: make(chan struct{}),
		contexts: map[proto.BrowserBrowserContextID]*recorder{}, sessions: map[proto.TargetSessionID]*recorder{}}
	h.root = cdp{b: b.Browser, ctx: ctx}
	events := b.Context(ctx).Event()
	go h.loop(events)
	// Pages (tabs and popups) and the workers that aren't tied to one
	// frame attach here, paused, so a run sets up interception before
	// their first request. Frames and dedicated workers attach under
	// their page (recorder.setup).
	err := proto.TargetSetAutoAttach{AutoAttach: true, WaitForDebuggerOnStart: true, Flatten: true,
		Filter: proto.TargetTargetFilter{{Type: "page"}, {Type: "service_worker"}, {Type: "shared_worker"}, {Exclude: true}},
	}.Call(h.root)
	if err != nil {
		cancel()
		<-h.done
		return nil, fmt.Errorf("consent: watching Chrome's targets: %w", err)
	}
	return h, nil
}

// close stops routing.
func (h *hub) close() {
	h.cancel()
	<-h.done
}

// register routes the targets of browser context id to r.
func (h *hub) register(id proto.BrowserBrowserContextID, r *recorder) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.contexts[id] = r
}

// unregister forgets a context and its sessions.
func (h *hub) unregister(id proto.BrowserBrowserContextID) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := h.contexts[id]
	delete(h.contexts, id)
	for s, owner := range h.sessions {
		if owner == r {
			delete(h.sessions, s)
		}
	}
}

// own routes session s to r.
func (h *hub) own(s proto.TargetSessionID, r *recorder) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sessions[s] = r
}

// disown forgets session s.
func (h *hub) disown(s proto.TargetSessionID) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.sessions, s)
}

func (h *hub) bySession(s proto.TargetSessionID) *recorder {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sessions[s]
}

func (h *hub) byContext(id proto.BrowserBrowserContextID) *recorder {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.contexts[id]
}

// loop reads every event once. Browser-level attachments are routed by
// their context; everything else by the session it arrived on. The
// recorder only queues the event, so one busy run never holds up the
// others.
func (h *hub) loop(events <-chan *rod.Message) {
	defer close(h.done)
	for msg := range events {
		h.dispatch(msg)
	}
}

func (h *hub) dispatch(msg *rod.Message) {
	defer func() { _ = recover() }() // rod's Load panics on an event it can't decode
	if msg.SessionID == "" {
		switch msg.Method {
		case "Target.attachedToTarget":
			var e proto.TargetAttachedToTarget
			if !msg.Load(&e) || e.TargetInfo == nil {
				return
			}
			if r := h.byContext(e.TargetInfo.BrowserContextID); r != nil {
				h.own(e.SessionID, r)
				r.attached("", e)
				return
			}
			h.release(e)
		case "Target.detachedFromTarget":
			var e proto.TargetDetachedFromTarget
			if msg.Load(&e) {
				if r := h.bySession(e.SessionID); r != nil {
					r.detached(e.SessionID)
				}
				h.disown(e.SessionID)
			}
		case "Browser.downloadWillBegin":
			// Downloads are denied per context; the event names no
			// context, so every run with a matching frame hears it.
			var e proto.BrowserDownloadWillBegin
			if msg.Load(&e) {
				h.mu.Lock()
				rs := make([]*recorder, 0, len(h.contexts))
				for _, r := range h.contexts {
					rs = append(rs, r)
				}
				h.mu.Unlock()
				for _, r := range rs {
					r.download(e)
				}
			}
		}
		return
	}
	r := h.bySession(msg.SessionID)
	if r == nil {
		return
	}
	if msg.Method == "Target.attachedToTarget" {
		var e proto.TargetAttachedToTarget
		if msg.Load(&e) && e.TargetInfo != nil {
			h.own(e.SessionID, r)
			r.attached(msg.SessionID, e)
		}
		return
	}
	r.event(msg)
}

// release lets a target no run owns go on: rod's own tabs, the default
// context. It was attached paused, so it is resumed. The session stays
// attached and unread: detaching right after the resume leaves Chrome
// holding the commands other sessions sent while it was paused.
func (h *hub) release(e proto.TargetAttachedToTarget) {
	if !e.WaitingForDebugger {
		return
	}
	go func() {
		_ = proto.RuntimeRunIfWaitingForDebugger{}.Call(cdp{b: h.root.b, id: e.SessionID, ctx: h.root.ctx})
	}()
}
