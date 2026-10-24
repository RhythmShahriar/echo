// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package runner

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/RhythmShahriar/echo/check"
	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/model"
)

// connectors are the Source.Via of messages a site's connector sent. A
// connector tags everything it sends while handling an action with the
// action's X-Echo-Run, so Echo never needs the open action to tell whose
// such mail is. Plain SMTP, sendmail and the proxy only carry a tag
// when something else put it there.
var connectors = []string{"wordpress", "laravel", "node"}

// tagged reports whether m came from a connector, so it named its action.
func tagged(m model.Message) bool {
	return m.Run != "" && slices.Contains(connectors, m.Source.Via)
}

// finished is what runAll hands back to Run.
type finished struct {
	lost       map[string]bool      // IDs of messages Echo couldn't credit to any action
	ended      map[string]time.Time // when each action stopped waiting, by ID
	overlapped bool                 // two of this run's actions were open at once
}

// runAll runs actions and puts them in run.Actions in config order,
// leaving out any a canceled ctx kept from starting.
//
// With Options.Parallel above 1 several actions run at once, which only
// works when every message names its action: untagged mail (plain SMTP)
// that arrives while two actions are open is credited to neither. So:
//   - until a connector-tagged message of this run shows up, actions run
//     one by one, the way they do without Parallel. Once the first of
//     them has finished without one, the rest take exclusive leases, so
//     another run can't share Echo with them either;
//   - after one shows up, up to Parallel actions run at once with shared
//     leases, the next starting as soon as one finishes;
//   - an action with parallel: false waits until every other action has
//     ended, runs alone with an exclusive lease and holds back the next.
//
// Actions still start in config order. Without Parallel nothing changes:
// one by one, shared leases.
//
// Parallel also needs Echo's leases (catcher C5). Without them Echo keeps
// one open action for everybody, so each Begin takes the untagged mail
// of the action before it, with nothing to warn about. A client without
// leases runs one by one and says so in the run's warnings.
func runAll(ctx context.Context, o Options, run *model.Run, actions []config.Action, fake *fakeData, opts check.Options) finished {
	width := max(o.Parallel, 1)
	if width > 1 && !o.leases() {
		width = 1
		run.Warnings = append(run.Warnings, noLeasesWarning)
	}
	g := newGate(ctx, width)
	defer g.close()
	// Flush commands (wp cron event run, queue:work) run once per action.
	// Some queue runners break with two of them at once, so they take
	// turns even when actions don't.
	o.flush = &sync.Mutex{}
	if g.width > 1 {
		go g.watch(ctx, o.Client, run.ID)
	}
	results := make([]*model.Action, len(actions))
	out := finished{lost: map[string]bool{}, ended: map[string]time.Time{}}
	var mu sync.Mutex // guards out, results and the hooks, which may not expect two callers
	done := 0
	if logf := o.Hooks.Log; logf != nil {
		o.Hooks.Log = func(s string) {
			mu.Lock()
			defer mu.Unlock()
			logf(s)
		}
	}
	var wg sync.WaitGroup
	for i, a := range actions {
		alone := a.Parallel != nil && !*a.Parallel
		exclusive, ok := g.enter(alone)
		if !ok || ctx.Err() != nil {
			break // canceled
		}
		exclusive = exclusive && g.width > 1
		wg.Go(func() {
			act, unsure := runAction(ctx, o, run, a, fake, opts, exclusive)
			overlapped := g.leave(alone, act.Messages)
			mu.Lock()
			defer mu.Unlock()
			out.ended[act.ID] = time.Now()
			out.overlapped = out.overlapped || overlapped
			for _, id := range unsure {
				out.lost[id] = true
			}
			results[i] = &act
			done++
			if o.Hooks.Action != nil {
				o.Hooks.Action(results[i], done, len(actions))
			}
		})
	}
	wg.Wait()
	for _, a := range results {
		if a != nil {
			run.Actions = append(run.Actions, *a)
		}
	}
	return out
}

// noLeasesWarning is the run warning when Options.Parallel asked for
// parallel actions that this Echo can't keep apart.
const noLeasesWarning = "actions ran one by one: running them in parallel needs a newer Echo that keeps a lease per action"

// leases reports whether o.Client has the lease API (see leaser).
func (o Options) leases() bool {
	if o.assumeLeases {
		return true
	}
	_, ok := any(o.Client).(leaser)
	return ok
}

// gate decides when the next action may start; see runAll.
type gate struct {
	width int
	mu    sync.Mutex
	cond  *sync.Cond
	open  int  // actions running now
	alone bool // one of them runs alone (parallel: false)
	// tagged is set once a connector-tagged message of this run was
	// seen; until then actions run one by one.
	tagged bool
	// probed is set once an action finished before tagged was: the site
	// may send only untagged mail, so the rest take exclusive leases.
	probed bool
	// overlap is set once two actions were open at once.
	overlap  bool
	canceled bool
	stop     func() bool
	quit     chan struct{}
}

func newGate(ctx context.Context, width int) *gate {
	g := &gate{width: width, quit: make(chan struct{})}
	g.cond = sync.NewCond(&g.mu)
	g.stop = context.AfterFunc(ctx, func() {
		g.mu.Lock()
		g.canceled = true
		g.mu.Unlock()
		g.cond.Broadcast()
	})
	return g
}

// close stops the watcher and the cancel hook.
func (g *gate) close() {
	g.stop()
	close(g.quit)
}

// enter waits until the next action may start and counts it in. It
// says whether the action needs an exclusive lease, or false for ok when
// ctx was canceled first.
func (g *gate) enter(alone bool) (exclusive, ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for {
		if g.canceled {
			return false, false
		}
		free := g.open == 0
		if !alone && g.tagged && !g.alone {
			free = g.open < g.width
		}
		if free {
			break
		}
		g.cond.Wait()
	}
	if g.open > 0 {
		g.overlap = true
	}
	g.open++
	g.alone = alone
	return alone || (!g.tagged && g.probed), true
}

// leave counts a finished action out, learning from its messages whether
// the site tags its mail. It says whether this run's actions ever
// overlapped.
func (g *gate) leave(alone bool, msgs []model.Message) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.open--
	if alone {
		g.alone = false
	}
	if slices.ContainsFunc(msgs, tagged) {
		g.tagged = true
	}
	if !g.tagged {
		g.probed = true
	}
	g.cond.Broadcast()
	return g.overlap
}

// sawTag opens the gate to Parallel actions at once.
func (g *gate) sawTag() {
	g.mu.Lock()
	g.tagged = true
	g.mu.Unlock()
	g.cond.Broadcast()
}

// watchEvery is how often watch looks for a tagged message.
const watchEvery = 50 * time.Millisecond

// watch looks for this run's first connector-tagged message while the
// first actions run one by one, so the run widens as soon as the site
// shows it tags its mail rather than after a whole quiet window.
func (g *gate) watch(ctx context.Context, c *client.Client, run string) {
	t := time.NewTicker(watchEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-g.quit:
			return
		case <-t.C:
		}
		g.mu.Lock()
		known := g.tagged
		g.mu.Unlock()
		if known {
			return
		}
		msgs, err := c.Messages(ctx, client.Filter{Run: run})
		if err == nil && slices.ContainsFunc(msgs, tagged) {
			g.sawTag()
			return
		}
	}
}
