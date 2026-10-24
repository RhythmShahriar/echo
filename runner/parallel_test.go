// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/wire"
)

// leaseLog wraps a real Echo and records every begin and end the runner
// sends, in order.
type leaseLog struct {
	http.Handler
	mu     sync.Mutex
	events []string // "begin <action>" or "end"
}

func (l *leaseLog) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/v1/actions/begin":
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		var b struct{ Action string }
		_ = json.Unmarshal(body, &b)
		l.add("begin " + b.Action)
	case "/v1/actions/end":
		l.add("end")
	}
	l.Handler.ServeHTTP(w, r)
}

func (l *leaseLog) add(e string) {
	l.mu.Lock()
	l.events = append(l.events, e)
	l.mu.Unlock()
}

// widest is the most actions the log shows open at once.
func (l *leaseLog) widest() int {
	open, most := 0, 0
	for _, e := range l.events {
		if e == "end" {
			open--
		} else {
			open++
		}
		most = max(most, open)
	}
	return most
}

// echoWithLog starts a real Echo behind a leaseLog.
func echoWithLog(t *testing.T) (*catcher.Server, *leaseLog, *client.Client) {
	t.Helper()
	s := catcher.NewServer(catcher.NewInbox(1000), catcher.Config{Version: "test"})
	log := &leaseLog{Handler: s}
	srv := httptest.NewServer(log)
	t.Cleanup(srv.Close)
	return s, log, client.New(srv.URL, "")
}

// taggingSite emails from WordPress's connector, tagged with the
// request's run, the moment a request comes in. The subject is the path.
func taggingSite(t *testing.T, echo *client.Client) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := echo.Capture(context.Background(), wire.Capture{Kind: "email", Run: r.Header.Get(wire.RunHeader), Source: wire.Source{Via: "wordpress"},
			Email: &wire.Email{From: "shop@shop.test", To: []string{"buyer@shop.test"}, Subject: r.URL.Path, Text: "Hello"}}); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func httpActions(n int) []config.Action {
	var out []config.Action
	for i := range n {
		out = append(out, config.Action{Name: fmt.Sprintf("A%d", i+1), HTTP: &config.HTTPAction{URL: fmt.Sprintf("/a%d", i+1)}})
	}
	return out
}

func TestParallelTaggedActions(t *testing.T) {
	_, log, echo := echoWithLog(t)
	cfg := config.Default()
	cfg.Site = taggingSite(t, echo).URL
	cfg.Quiet, cfg.Timeout = config.Duration{Duration: 500 * time.Millisecond}, config.Duration{Duration: 10 * time.Second}
	cfg.Actions = httpActions(8)
	var progress []int
	start := time.Now()
	run, err := Run(context.Background(), Options{Config: cfg, Client: echo, Parallel: 4,
		Hooks: Hooks{Action: func(_ *model.Action, done, _ int) { progress = append(progress, done) }}})
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took >= 1500*time.Millisecond {
		t.Errorf("8 actions 4 at a time took %s", took)
	}
	if len(run.Actions) != 8 {
		t.Fatalf("%d actions", len(run.Actions))
	}
	for i, a := range run.Actions {
		if want := fmt.Sprintf("A%d", i+1); a.Label != want {
			t.Errorf("action %d is %s; results must keep config order", i, a.Label)
		}
		if len(a.Messages) != 1 || a.Messages[0].Subject != fmt.Sprintf("/a%d", i+1) || a.Messages[0].Action != a.ID {
			t.Errorf("%s got %+v", a.Label, a.Messages)
		}
	}
	if w := log.widest(); w < 2 || w > 4 {
		t.Errorf("up to 4 actions should run at once; saw %d", w)
	}
	if !slices.Equal(progress, []int{1, 2, 3, 4, 5, 6, 7, 8}) {
		t.Errorf("progress %v", progress)
	}
	if len(run.Warnings) != 0 {
		t.Errorf("warnings: %q", run.Warnings)
	}
}

func TestParallelFalseRunsAlone(t *testing.T) {
	_, log, echo := echoWithLog(t)
	cfg := config.Default()
	cfg.Site = taggingSite(t, echo).URL
	cfg.Quiet, cfg.Timeout = config.Duration{Duration: 200 * time.Millisecond}, config.Duration{Duration: 10 * time.Second}
	cfg.Actions = httpActions(7)
	no := false
	cfg.Actions[3].Parallel = &no // A4
	run, err := Run(context.Background(), Options{Config: cfg, Client: echo, Parallel: 4})
	if err != nil {
		t.Fatal(err)
	}
	open := 0
	for i, e := range log.events {
		if e == "begin action:a4" {
			if open != 0 {
				t.Errorf("A4 began with %d actions open: %q", open, log.events)
			}
			if i+1 >= len(log.events) || log.events[i+1] != "end" {
				t.Errorf("another action began while A4 was open: %q", log.events)
			}
		}
		if e == "end" {
			open--
		} else {
			open++
		}
	}
	if log.widest() < 2 {
		t.Errorf("the other actions should still overlap: %q", log.events)
	}
	for i, a := range run.Actions {
		if len(a.Messages) != 1 || a.Messages[0].Subject != fmt.Sprintf("/a%d", i+1) {
			t.Errorf("%s got %+v", a.Label, a.Messages)
		}
	}
}

// A site whose mail carries no tag (plain SMTP) never gets actions run
// beside each other: Echo could only credit the mail to the one open.
func TestUntaggedMailRunsOneByOne(t *testing.T) {
	s, log, echo := echoWithLog(t)
	shop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.Inbox.Add(model.Message{Channel: model.Email, Source: model.Source{Via: "smtp"}, Subject: r.URL.Path, Text: "Hello", To: []string{"buyer@shop.test"}})
	}))
	defer shop.Close()
	cfg := config.Default()
	cfg.Site = shop.URL
	cfg.Quiet, cfg.Timeout = config.Duration{Duration: 100 * time.Millisecond}, config.Duration{Duration: 10 * time.Second}
	cfg.Actions = httpActions(4)
	run, err := Run(context.Background(), Options{Config: cfg, Client: echo, Parallel: 4})
	if err != nil {
		t.Fatal(err)
	}
	if w := log.widest(); w != 1 {
		t.Errorf("untagged actions overlapped: %q", log.events)
	}
	for i, a := range run.Actions {
		if len(a.Messages) != 1 || a.Messages[0].Subject != fmt.Sprintf("/a%d", i+1) {
			t.Errorf("%s got %+v", a.Label, a.Messages)
		}
	}
}

func TestParallelStopsOnCancel(t *testing.T) {
	_, _, echo := echoWithLog(t)
	cfg := config.Default()
	cfg.Site = taggingSite(t, echo).URL
	cfg.Quiet = config.Duration{Duration: 100 * time.Millisecond}
	cfg.Actions = httpActions(6)
	ctx, cancel := context.WithCancel(context.Background())
	run, err := Run(ctx, Options{Config: cfg, Client: echo, Parallel: 2,
		Hooks: Hooks{Action: func(_ *model.Action, done, _ int) {
			if done == 1 {
				cancel()
			}
		}}})
	if err == nil {
		t.Fatal("a canceled run must say so")
	}
	if n := len(run.Actions); n == 0 || n == 6 {
		t.Errorf("%d actions ran; the ones not started must be left out", n)
	}
	for i, a := range run.Actions[1:] {
		if a.Label <= run.Actions[i].Label {
			t.Errorf("out of order: %s after %s", a.Label, run.Actions[i].Label)
		}
	}
}

// exclusiveEcho also has catcher C15's exclusive lease.
type exclusiveEcho struct{ leasingEcho }

func (f *exclusiveEcho) BeginExclusive(_ context.Context, run, action string) (string, error) {
	f.calls = append(f.calls, "exclusive "+run+"/"+action)
	return "X-" + action, nil
}

func TestBeginExclusive(t *testing.T) {
	x := &exclusiveEcho{}
	begin(context.Background(), x, "r1", "a", true)()
	begin(context.Background(), x, "r1", "b", false)()
	if want := []string{"exclusive r1/a", "end X-a", "lease r1/b", "end L-b"}; !slices.Equal(x.calls, want) {
		t.Errorf("calls %q, want %q", x.calls, want)
	}
}

// The gate's lease choice: shared while probing the first action, then
// exclusive while the site shows no tag; shared once it does, except
// for parallel: false.
func TestGateLeases(t *testing.T) {
	g := newGate(context.Background(), 4)
	defer g.close()
	var got []string
	step := func(alone bool, msgs ...model.Message) {
		x, ok := g.enter(alone)
		if !ok {
			t.Fatal("canceled")
		}
		got = append(got, fmt.Sprint(x))
		g.leave(alone, msgs)
	}
	step(false) // probe: nothing tagged
	step(false) // the site sent no tags: exclusive
	step(true)
	step(false, model.Message{Run: "r", Source: model.Source{Via: "laravel"}})
	step(false) // tagged now: shared
	step(true)
	if want := "false true true true false true"; strings.Join(got, " ") != want {
		t.Errorf("exclusive: %s, want %s", strings.Join(got, " "), want)
	}
}

func TestTagged(t *testing.T) {
	for m, want := range map[*model.Message]bool{
		{Run: "r", Source: model.Source{Via: "wordpress"}}: true,
		{Run: "r", Source: model.Source{Via: "node"}}:      true,
		{Run: "r", Source: model.Source{Via: "smtp"}}:      false, // credited by the open action
		{Source: model.Source{Via: "laravel"}}:             false, // sent outside any action
	} {
		if tagged(*m) != want {
			t.Errorf("tagged(%+v) = %v", *m, !want)
		}
	}
}
