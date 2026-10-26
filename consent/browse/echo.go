// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browse

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/model"
)

// echoWait bounds each call to the Echo API, so an Echo that hangs
// costs a run seconds, not its whole --timeout.
const echoWait = 5 * time.Second

// echoLink is --echo for one check: the Echo API every run asks what
// the site sent server-side while it ran (PRD 13.6 step 7).
type echoLink struct {
	api  *client.Client
	url  string // masked, for the note
	down atomic.Bool
}

// newEchoLink is the check's link to Echo, nil without --echo.
func newEchoLink(l *consent.EchoLink) *echoLink {
	if l == nil {
		return nil
	}
	api := client.New(l.URL, l.Token)
	api.HTTP.Timeout = echoWait
	return &echoLink{api: api, url: consent.MaskURL(api.URL, false)}
}

// echoAction is the action a run is filed under in Echo: page-1-reject.
func echoAction(page int, kind consent.RunKind) string {
	return fmt.Sprintf("page-%d-%s", page, kind)
}

// echoSession is one run's action in Echo.
type echoSession struct {
	l      *echoLink
	run    string // Plan.ID
	action string
	lease  string
	since  time.Time
}

// begin opens the run's action in Echo and returns the session and the
// X-Echo-Run value the run's same-site requests carry. An Echo that
// doesn't answer is noted once on the result and the check goes on
// without it.
func (c *check) echoBegin(ctx context.Context, page int, kind consent.RunKind) (*echoSession, string) {
	l := c.echo
	if l == nil || l.down.Load() {
		return nil, ""
	}
	s := &echoSession{l: l, run: c.plan.ID, action: echoAction(page, kind), since: time.Now()}
	bctx, cancel := context.WithTimeout(ctx, echoWait)
	defer cancel()
	lease, err := l.api.BeginLease(bctx, s.run, s.action)
	if err != nil {
		if ctx.Err() == nil {
			c.echoDown()
		}
		return nil, ""
	}
	s.lease = lease
	return s, client.RunValue(s.run, s.action)
}

// echoDown notes once that Echo didn't answer; Echo isn't asked again.
func (c *check) echoDown() {
	if c.echo.down.CompareAndSwap(false, true) {
		c.note("Echo at " + c.echo.url + " didn't answer; server-side tracking wasn't checked")
	}
}

// echoEnd ends the run's action and lists the analytics messages Echo
// caught under it: GA4 Measurement Protocol and Meta Conversions API
// calls the site made from its server, each placed on the run's clock
// and in its phase. rec is the closed run's recording.
func (c *check) echoEnd(ctx context.Context, s *echoSession, a *attempt, rec *Recording) []consent.EchoMessage {
	if s == nil {
		return nil
	}
	ectx, cancel := context.WithTimeout(context.WithoutCancel(ctx), echoWait)
	defer cancel()
	if err := s.l.api.EndLease(ectx, s.lease); err != nil {
		c.echoDown()
		return nil
	}
	if ctx.Err() != nil || a.run == nil || rec == nil {
		return nil // canceled, or the run never started
	}
	msgs, err := s.l.api.Messages(ectx, client.Filter{Run: s.run, Action: s.action, Channel: model.Analytics, Since: s.since})
	if err != nil {
		c.echoDown()
		return nil
	}
	out := make([]consent.EchoMessage, 0, len(msgs))
	for _, m := range msgs {
		at := int64(0)
		if !rec.Start.IsZero() {
			at = m.At.Sub(rec.Start).Milliseconds()
		}
		out = append(out, consent.EchoMessage{
			ID:       consent.SafeText(m.ID, 100),
			Provider: echoProvider(m.Provider),
			Event:    consent.SafeText(m.Event, 100),
			AtMS:     at,
			Phase:    a.run.s.rec.phaseAt(at),
		})
	}
	return out
}

// echoProvider names a server-side provider as consent results do:
// Echo's ga4 is the GA4 Measurement Protocol (ga4-mp); meta-capi stays.
func echoProvider(p string) string {
	if p == "ga4" {
		return "ga4-mp"
	}
	return consent.SafeText(p, 60)
}
