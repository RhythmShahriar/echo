// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package runner

import (
	"context"
	"encoding/json"
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

// fakeEcho records begin and end calls.
type fakeEcho struct {
	calls []string
}

func (f *fakeEcho) Begin(_ context.Context, run, action string) error {
	f.calls = append(f.calls, "begin "+run+"/"+action)
	return nil
}

func (f *fakeEcho) End(context.Context) error {
	f.calls = append(f.calls, "end")
	return nil
}

// leasingEcho also has the lease API.
type leasingEcho struct{ fakeEcho }

func (f *leasingEcho) BeginLease(_ context.Context, run, action string) (string, error) {
	f.calls = append(f.calls, "lease "+run+"/"+action)
	return "L-" + action, nil
}

func (f *leasingEcho) EndLease(_ context.Context, lease string) error {
	f.calls = append(f.calls, "end "+lease)
	return nil
}

func TestBeginEndsItsOwnLease(t *testing.T) {
	l := &leasingEcho{}
	begin(context.Background(), l, "r1", "a")()
	if want := []string{"lease r1/a", "end L-a"}; !slices.Equal(l.calls, want) {
		t.Errorf("with leases: %q, want %q", l.calls, want)
	}
	// An end must reach Echo even when the run was canceled.
	ctx, cancel := context.WithCancel(context.Background())
	end := begin(ctx, &l.fakeEcho, "r1", "b")
	cancel()
	end()
	if want := []string{"lease r1/a", "end L-a", "begin r1/b", "end"}; !slices.Equal(l.calls, want) {
		t.Errorf("without leases: %q, want %q", l.calls, want)
	}
}

func TestAmbiguityWarning(t *testing.T) {
	var since []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/health":
			_ = json.NewEncoder(w).Encode(map[string]any{"tool": "rhythm-echo", "mode": "seal"})
		case "/v1/wait":
			_, _ = w.Write([]byte("[]"))
		case "/v1/messages":
			if r.Header.Get(wire.TokenHeader) != "t0ken" {
				http.Error(w, "token", http.StatusUnauthorized)
				return
			}
			if q := r.URL.Query(); q.Has("since") { // not the end-of-run list
				since = append(since, q.Get("since"))
			}
			// The same ambiguous message falls in both actions' windows
			// here; it counts once.
			_, _ = w.Write([]byte(`[{"id":"m1","attribution":"ambiguous"},{"id":"m2","run":"r"},{"id":"m3","attribution":"ambiguous"},{"id":"m4","attribution":"late"}]`))
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()
	cfg := config.Default()
	cfg.Quiet = config.Duration{Duration: 10 * time.Millisecond}
	cfg.Actions = []config.Action{{Name: "A", Command: "true"}, {Name: "B", Command: "true"}}
	run, err := Run(context.Background(), Options{Config: cfg, Client: client.New(srv.URL, "t0ken")})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"2 messages could not be tied to an action because another run was open"}; !slices.Equal(run.Warnings, want) {
		t.Errorf("warnings: %q, want %q", run.Warnings, want)
	}
	if len(since) != 2 || since[0] == "" || since[0] > since[1] {
		t.Errorf("each action asks from its own start: %q", since)
	}
	if ambiguityWarning(0) != "" || ambiguityWarning(1) != "1 message could not be tied to an action because another run was open" {
		t.Error("ambiguityWarning wording")
	}
}

// Two runs at once against one real Echo, with mail the site tags and
// mail it can't (plain SMTP): tagged mail always stays with the run that
// caused it. With catcher leases, the untagged mail is credited to
// nobody and both runs warn about it.
func TestConcurrentRunsKeepTheirMail(t *testing.T) {
	s := catcher.NewServer(catcher.NewInbox(100), catcher.Config{Version: "test"})
	srv := httptest.NewServer(s)
	defer srv.Close()
	echo := client.New(srv.URL, "")

	// Both requests must be in progress before either sends, so both
	// runs have an action open when the untagged mail arrives.
	var arrived sync.WaitGroup
	arrived.Add(2)
	both := make(chan struct{})
	go func() { arrived.Wait(); close(both) }()
	shop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived.Done()
		select {
		case <-both:
		case <-time.After(5 * time.Second):
			t.Error("the two runs never overlapped")
		}
		run := r.Header.Get(wire.RunHeader)
		if _, err := echo.Capture(context.Background(), wire.Capture{Kind: "email", Run: run, Source: wire.Source{Via: "wordpress"},
			Email: &wire.Email{To: []string{"buyer@shop.test"}, Subject: "Order " + run}}); err != nil {
			t.Error(err)
		}
		s.Inbox.Add(model.Message{Channel: model.Email, Source: model.Source{Via: "smtp"}, Subject: "Receipt", To: []string{"buyer@shop.test"}})
	}))
	defer shop.Close()

	cfg := config.Default()
	cfg.Site = shop.URL
	cfg.Quiet, cfg.Timeout = config.Duration{Duration: 300 * time.Millisecond}, config.Duration{Duration: 10 * time.Second}
	cfg.Actions = []config.Action{{Name: "Checkout", HTTP: &config.HTTPAction{URL: "/checkout"}}}
	runs := make([]*model.Run, 2)
	var wg sync.WaitGroup
	for i := range runs {
		wg.Go(func() {
			r, err := Run(context.Background(), Options{Config: cfg, Client: echo, Version: "test"})
			if err != nil {
				t.Error(err)
			}
			runs[i] = r
		})
	}
	wg.Wait()
	if runs[0] == nil || runs[1] == nil {
		t.FailNow()
	}
	_, leases := any(echo).(leaser)
	for _, r := range runs {
		act := r.Action("action:checkout")
		tagged := 0
		for _, m := range act.Messages {
			if m.Run != r.ID {
				t.Errorf("run %s was credited with %s's message %q", r.ID, m.Run, m.Subject)
			}
			if strings.HasPrefix(m.Subject, "Order ") {
				tagged++
				if m.Subject != "Order "+r.ID+"/action:checkout" {
					t.Errorf("run %s got %q", r.ID, m.Subject)
				}
			}
		}
		if tagged != 1 {
			t.Errorf("run %s: %d tagged messages, want 1", r.ID, tagged)
		}
		if leases && !slices.Equal(r.Warnings, []string{"2 messages could not be tied to an action because another run was open"}) {
			t.Errorf("run %s warnings: %q", r.ID, r.Warnings)
		}
	}
}
