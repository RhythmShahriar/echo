// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browse

import (
	_ "embed"
	"encoding/json"
	"errors"
	"math"
	"strings"

	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/echo/consent"
)

// hooksJS is the page-side half of the hooks: a function expression
// that takes its settings (hooksSource calls it).
//
//go:embed js/hooks.js
var hooksJS string

// bindingName is the CDP binding hooks.js reports through. The page can
// call it too, so every record it carries is untrusted input.
const bindingName = "__rhythmEchoConsent"

// unreadableHook is the note a hook record Echo can't read becomes.
const unreadableHook = "unreadable hook record"

// maxHookSkew bounds how far before its arrival a hook record's own
// time may lie. A record is delivered within milliseconds; an older
// time is the page's invention and could move a consent update to
// before a hit it followed.
const maxHookSkew = 10_000

// maxDroppedReport bounds one "dropped" record, which the page could
// also send.
const maxDroppedReport = 1_000_000

// hookSettings are the limits hooks.js keeps in each document.
type hookSettings struct {
	Binding    string `json:"binding"`
	MaxRecords int    `json:"maxRecords"`
	MaxBytes   int    `json:"maxBytes"`
}

// hooksSource is hooks.js called with the run's limits.
func hooksSource(l consent.Limits) string {
	cfg, _ := json.Marshal(hookSettings{Binding: bindingName, MaxRecords: l.HookRecords, MaxBytes: l.HookBytes})
	return strings.TrimRight(hooksJS, "\n") + "(" + string(cfg) + ");\n"
}

// installHooks adds the binding and hooks.js to a page or frame target
// before it runs: the script runs in the main world of every document
// the target loads, before the page's own scripts.
func (r *recorder) installHooks(t *target) error {
	if err := (proto.RuntimeAddBinding{Name: bindingName}).Call(t.s); err != nil {
		return err
	}
	// Chrome exposes a binding to the documents a target loads later only
	// while its Runtime domain is on.
	if err := (proto.RuntimeEnable{}).Call(t.s); err != nil {
		return err
	}
	_, err := proto.PageAddScriptToEvaluateOnNewDocument{Source: hooksSource(r.limits()), RunImmediately: true}.Call(t.s)
	return err
}

// hookHead is the part of a record Go reads itself.
type hookHead struct {
	T     float64 `json:"t"`
	Src   string  `json:"src"`
	N     int64   `json:"n"`
	Event string  `json:"event"`
	TC    string  `json:"tcString"`
	GPP   string  `json:"gppString"`
}

// hook handles one binding call. Records past Limits.HookRecords or
// Limits.HookBytes are counted and dropped; a record that isn't
// readable is a note, never an error. A consent signal becomes an
// EvSignal on the run clock; a click record notes the click's time.
func (r *recorder) hook(payload string) {
	var head hookHead
	readable := len(payload) <= consent.MaxHookRecord && json.Unmarshal([]byte(payload), &head) == nil
	if readable && head.Src == "dropped" {
		// hooks.js stopped sending in a document that sent its share.
		n := min(max(head.N, 0), maxDroppedReport)
		if n > 0 {
			r.dropped.Add(n)
			r.trunc.Store(true)
		}
		return
	}
	l := r.limits()
	r.mu.Lock()
	r.hookN++
	r.hookBytes += len(payload)
	over := r.hookN > l.HookRecords || r.hookBytes > l.HookBytes
	r.mu.Unlock()
	if over {
		r.dropped.Add(1)
		r.trunc.Store(true)
		return
	}
	if !readable {
		r.emit(consent.Event{Kind: consent.EvNote, AtMS: r.now(), Note: unreadableHook})
		return
	}
	at := r.hookTime(head.T)
	var sig *consent.Signal
	switch head.Src {
	case "click":
		r.noteClick(at)
		return
	case "tcf":
		sig = r.tcfSignal(head)
	case "gpp":
		sig = r.gppSignal(head)
	default:
		var err error
		sig, err = r.decodeHook(payload)
		if err != nil {
			r.emit(consent.Event{Kind: consent.EvNote, AtMS: at, Note: unreadableHook})
			return
		}
	}
	if sig != nil {
		r.emit(consent.Event{Kind: consent.EvSignal, AtMS: at, Signal: sig})
	}
}

// decodeHook asks the signal decoder; a decoder that panics makes the
// record unreadable rather than the run fail.
func (r *recorder) decodeHook(payload string) (sig *consent.Signal, err error) {
	dec := r.stages.Signals
	if dec == nil {
		return nil, nil
	}
	defer func() {
		if recover() != nil {
			sig, err = nil, errHookPanic
		}
	}()
	return dec.Hook([]byte(payload))
}

// errHookPanic stands for a decoder that panicked on a record.
var errHookPanic = errors.New("hook decoder failed")

// tcfSignal is a TCF event with its TC string decoded when the decoder
// can read it.
func (r *recorder) tcfSignal(h hookHead) *consent.Signal {
	switch h.Event {
	case "tcloaded", "cmpuishown", "useractioncomplete":
	default:
		return nil
	}
	s := &consent.Signal{Source: "tcf", Command: h.Event}
	if dec := r.stages.Signals; dec != nil && h.TC != "" {
		func() {
			defer func() { _ = recover() }()
			if tc, err := dec.TCF(h.TC); err == nil {
				s.TCF = tc
			}
		}()
	}
	return s
}

// gppSignal is a GPP event with its string decoded when the decoder can
// read it.
func (r *recorder) gppSignal(h hookHead) *consent.Signal {
	ev := consent.SafeText(h.Event, 64)
	if ev == "" {
		return nil
	}
	s := &consent.Signal{Source: "gpp", Command: ev}
	if dec := r.stages.Signals; dec != nil && h.GPP != "" {
		func() {
			defer func() { _ = recover() }()
			if g, err := dec.GPP(h.GPP); err == nil {
				s.GPP = g
			}
		}()
	}
	return s
}

// hookTime puts a record's page time on the run clock, kept between
// maxHookSkew before now (and not before the first navigation) and now:
// the page writes the time and must not be able to move a record far
// from when it arrived.
func (r *recorder) hookTime(t float64) int64 {
	now := r.now()
	if t <= 0 || math.IsNaN(t) || math.IsInf(t, 0) {
		return now
	}
	at := r.atPage(t)
	lo := max(now-maxHookSkew, 0)
	switch {
	case at > now:
		return now
	case at < lo:
		return min(lo, now)
	}
	return at
}

// noteClick keeps the time of a pointerdown the page saw, for the Tab's
// click to read.
func (r *recorder) noteClick(at int64) {
	select {
	case r.clicks <- at:
	default:
		// Nobody is waiting for clicks: forget the oldest.
		select {
		case <-r.clicks:
		default:
		}
		select {
		case r.clicks <- at:
		default:
		}
	}
}
