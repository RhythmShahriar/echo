// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package runner

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
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

// asyncSite answers at once and sends what plan says, each after its
// delay and tagged with the request's run, like a queued sender.
func asyncSite(t *testing.T, echo *client.Client, plan map[time.Duration]wire.Capture) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		run := r.Header.Get(wire.RunHeader)
		for after, c := range plan {
			time.AfterFunc(after, func() {
				c.Run, c.Source = run, wire.Source{Via: "wordpress"}
				if _, err := echo.Capture(context.Background(), c); err != nil {
					t.Error(err)
				}
			})
		}
	}))
}

func email(subject string) wire.Capture {
	return wire.Capture{Kind: "email", Email: &wire.Email{From: "shop@shop.test", To: []string{"buyer@shop.test"}, Subject: subject, Text: "Thanks for your order."}}
}

func TestExpectFinishesEarly(t *testing.T) {
	srv := httptest.NewServer(catcher.NewServer(catcher.NewInbox(100), catcher.Config{Version: "test"}))
	defer srv.Close()
	echo := client.New(srv.URL, "")
	shop := asyncSite(t, echo, map[time.Duration]wire.Capture{100 * time.Millisecond: email("Receipt")})
	defer shop.Close()

	cfg := config.Default()
	cfg.Site = shop.URL
	cfg.Quiet, cfg.Timeout = config.Duration{Duration: 2 * time.Second}, config.Duration{Duration: 10 * time.Second}
	cfg.Actions = []config.Action{{Name: "Checkout", HTTP: &config.HTTPAction{URL: "/"}, Expect: config.Expect{model.Email: {N: 1}}}}
	start := time.Now()
	run, err := Run(context.Background(), Options{Config: cfg, Client: echo})
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took >= 500*time.Millisecond {
		t.Errorf("took %s; a met expect must not wait the 2s quiet", took)
	}
	if a := run.Actions[0]; len(a.Messages) != 1 || len(a.Issues) != 0 || a.Error != "" {
		t.Errorf("action: %+v", a)
	}
}

// Echo's wait counts every channel: a webhook reaching the count first
// must not end the wait before the expected email.
func TestExpectWaitsForItsChannel(t *testing.T) {
	srv := httptest.NewServer(catcher.NewServer(catcher.NewInbox(100), catcher.Config{Version: "test"}))
	defer srv.Close()
	echo := client.New(srv.URL, "")
	hook := wire.Capture{Kind: "http", Phase: wire.Before, HTTP: &wire.HTTP{Method: "POST", URL: "https://hooks.zapier.com/hooks/catch/1/x/", Body: "{}"}}
	shop := asyncSite(t, echo, map[time.Duration]wire.Capture{0: hook, 500 * time.Millisecond: email("Receipt")})
	defer shop.Close()

	cfg := config.Default()
	cfg.Site = shop.URL
	cfg.Quiet, cfg.Timeout = config.Duration{Duration: 2 * time.Second}, config.Duration{Duration: 10 * time.Second}
	cfg.Actions = []config.Action{{Name: "Checkout", HTTP: &config.HTTPAction{URL: "/"}, Expect: config.Expect{model.Email: {N: 1}}}}
	start := time.Now()
	run, err := Run(context.Background(), Options{Config: cfg, Client: echo})
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took >= 1500*time.Millisecond {
		t.Errorf("took %s", took)
	}
	a := run.Actions[0]
	var chans []model.Channel
	for _, m := range a.Messages {
		chans = append(chans, m.Channel)
	}
	if !slices.Contains(chans, model.Email) || len(a.Issues) != 0 {
		t.Errorf("messages %v, issues %+v", chans, a.Issues)
	}
}

// waitEcho is a fake Echo that answers each wait at once with its
// action's messages and records the waits asked for.
type waitEcho struct {
	mu    sync.Mutex
	waits []string                     // "action quiet timeout count"
	msgs  map[string][]json.RawMessage // per action, for /v1/wait
	list  func() string                // the run's messages, for /v1/messages
}

func (f *waitEcho) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	switch r.URL.Path {
	case "/v1/health":
		_ = json.NewEncoder(w).Encode(map[string]any{"tool": "rhythm-echo", "mode": "seal"})
	case "/v1/wait":
		f.mu.Lock()
		f.waits = append(f.waits, q.Get("action")+" "+q.Get("quiet")+" "+q.Get("count"))
		msgs := f.msgs[q.Get("action")]
		f.mu.Unlock()
		if msgs == nil {
			msgs = []json.RawMessage{}
		}
		_ = json.NewEncoder(w).Encode(msgs)
	case "/v1/messages":
		if q.Has("since") || f.list == nil {
			_, _ = w.Write([]byte("[]"))
			return
		}
		_, _ = w.Write([]byte(f.list()))
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func dur(d time.Duration) *config.Duration { return &config.Duration{Duration: d} }

func TestQuietPerActionAndOverrides(t *testing.T) {
	fake := &waitEcho{msgs: map[string][]json.RawMessage{"action:with-expect": {json.RawMessage(`{"id":"m1","channel":"email"}`)}}}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	cfg := config.Default()
	cfg.Quiet = config.Duration{Duration: time.Second}
	cfg.Actions = []config.Action{
		{Name: "Own quiet", Command: "true", Quiet: dur(50 * time.Millisecond)},
		{Name: "Global quiet", Command: "true"},
		{Name: "With expect", Command: "true", Expect: config.Expect{model.Email: {N: 1}}},
		{Name: "Short expect", Command: "true", Quiet: dur(100 * time.Millisecond), Expect: config.Expect{model.Email: {N: 1}}},
	}
	for _, tc := range []struct {
		quiet time.Duration
		want  []string
	}{
		{0, []string{"action:own-quiet 50ms 0", "action:global-quiet 1s 0", "action:with-expect 200ms 1", "action:short-expect 100ms 1"}},
		{300 * time.Millisecond, []string{"action:own-quiet 300ms 0", "action:global-quiet 300ms 0", "action:with-expect 200ms 1", "action:short-expect 200ms 1"}},
	} {
		fake.waits = nil
		if _, err := Run(context.Background(), Options{Config: cfg, Client: client.New(srv.URL, ""), Quiet: tc.quiet}); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(fake.waits, tc.want) {
			t.Errorf("Options.Quiet %s: waits %q, want %q", tc.quiet, fake.waits, tc.want)
		}
	}
}

func TestTimeoutOverride(t *testing.T) {
	var timeouts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/health":
			_ = json.NewEncoder(w).Encode(map[string]any{"tool": "rhythm-echo", "mode": "seal"})
		case "/v1/wait":
			timeouts = append(timeouts, r.URL.Query().Get("timeout"))
			_, _ = w.Write([]byte("[]"))
		case "/v1/messages":
			_, _ = w.Write([]byte("[]"))
		}
	}))
	defer srv.Close()
	cfg := config.Default()
	cfg.Actions = []config.Action{{Name: "A", Command: "true"}}
	if _, err := Run(context.Background(), Options{Config: cfg, Client: client.New(srv.URL, ""), Timeout: 5 * time.Second}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(timeouts, []string{"5s"}) {
		t.Errorf("timeouts %q", timeouts)
	}
}

func TestWaitOptionsRefused(t *testing.T) {
	cfg := config.Default()
	cfg.Actions = []config.Action{{Name: "Slow", Command: "true", Quiet: dur(10 * time.Second)}}
	for _, tc := range []struct {
		o    Options
		want string
	}{
		{Options{Timeout: 2 * time.Minute}, "timeout 2m is over the 1m Echo waits for one action at most"},
		{Options{Quiet: 45 * time.Second}, "quiet 45s is over the 30s Echo waits for quiet at most"},
		{Options{Quiet: 20 * time.Second, Timeout: 10 * time.Second}, "quiet 20s is longer than the timeout of 10s, so no action could ever fall quiet"},
		{Options{Timeout: time.Second}, "quiet 2s is longer than the timeout of 1s, so no action could ever fall quiet"},
		{Options{Timeout: 5 * time.Second}, `action "Slow": quiet 10s is longer than the timeout of 5s, so it could never fall quiet`},
	} {
		tc.o.Config = cfg
		tc.o.Client = client.New("http://127.0.0.1:20420", "") // never reached
		_, err := Run(context.Background(), tc.o)
		if !errors.Is(err, ErrWaits) || err.Error() != tc.want {
			t.Errorf("%+v: %v, want %q", tc.o, err, tc.want)
		}
	}
	// Options.Quiet replaces the action's quiet, so that one is fine.
	if _, err := (Options{Config: cfg, Quiet: time.Second, Timeout: 5 * time.Second}).waits(); err != nil {
		t.Error(err)
	}
}

// Untagged mail Echo marks late (catcher C9) belongs to the action that
// ended before it arrived, not to the one open when it came.
func TestLateMailGoesToItsAction(t *testing.T) {
	fake := &waitEcho{msgs: map[string][]json.RawMessage{}}
	fake.list = func() string {
		// Arrived just now, after Receipt's action ended.
		at := time.Now().UTC().Format(time.RFC3339Nano)
		return `[{"id":"m1","channel":"email","subject":"Receipt","at":"` + at + `","attribution":"late","late_for":"action:checkout","run":"r"},` +
			`{"id":"m2","channel":"email","subject":"Welcome","action":"action:sign-up","at":"` + at + `"},` +
			`{"id":"m3","channel":"email","subject":"Lost","attribution":"late","late_for":"action:gone","at":"` + at + `"}]`
	}
	fake.msgs["action:sign-up"] = []json.RawMessage{json.RawMessage(`{"id":"m2","channel":"email","subject":"Welcome","action":"action:sign-up"}`)}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	cfg := config.Default()
	cfg.Quiet = config.Duration{Duration: 10 * time.Millisecond}
	cfg.Timeout = config.Duration{Duration: time.Second}
	cfg.Actions = []config.Action{
		{Name: "Checkout", Command: "true", Expect: config.Expect{model.Email: {N: 1}}},
		{Name: "Sign up", Command: "true"},
	}
	run, err := Run(context.Background(), Options{Config: cfg, Client: client.New(srv.URL, "")})
	if err != nil {
		t.Fatal(err)
	}
	checkout, signup := run.Action("action:checkout"), run.Action("action:sign-up")
	if len(checkout.Messages) != 1 || checkout.Messages[0].ID != "m1" || checkout.Messages[0].Action != "action:checkout" {
		t.Errorf("checkout: %+v", checkout.Messages)
	}
	for _, i := range checkout.Issues {
		if strings.HasPrefix(i.Check, "expect/") {
			t.Errorf("the late email meets the expectation; issue %q", i.Title)
		}
	}
	if len(signup.Messages) != 1 || signup.Messages[0].ID != "m2" {
		t.Errorf("sign up: %+v", signup.Messages)
	}
	if len(run.Warnings) != 2 {
		t.Fatalf("warnings: %q", run.Warnings)
	}
	if !regexp.MustCompile(`^Checkout: email message m1 arrived \d+\.\d s after the action ended and is counted with it$`).MatchString(run.Warnings[0]) {
		t.Errorf("note: %q", run.Warnings[0])
	}
	if !strings.HasPrefix(run.Warnings[1], "Some mail arrived after its action ended; add a flush: command") {
		t.Errorf("run note: %q", run.Warnings[1])
	}

	// With flush commands set, the note says to check them instead.
	cfg.Flush = []string{"true"}
	run, err = Run(context.Background(), Options{Config: cfg, Client: client.New(srv.URL, "")})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(run.Warnings); n == 0 || !strings.Contains(run.Warnings[n-1], "although the flush: commands ran") {
		t.Errorf("warnings: %q", run.Warnings)
	}
}

// No late mail: no notes; the actions keep what they had.
func TestNoLateMailNoNotes(t *testing.T) {
	fake := &waitEcho{msgs: map[string][]json.RawMessage{"action:a": {json.RawMessage(`{"id":"m1","channel":"email","action":"action:a"}`)}}}
	fake.list = func() string { return `[{"id":"m1","channel":"email","action":"action:a"}]` }
	srv := httptest.NewServer(fake)
	defer srv.Close()
	cfg := config.Default()
	cfg.Quiet = config.Duration{Duration: 10 * time.Millisecond}
	cfg.Actions = []config.Action{{Name: "A", Command: "true"}}
	run, err := Run(context.Background(), Options{Config: cfg, Client: client.New(srv.URL, "")})
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Warnings) != 0 || len(run.Actions[0].Messages) != 1 {
		t.Errorf("warnings %q, messages %+v", run.Warnings, run.Actions[0].Messages)
	}
}

// Mail the site tagged with an action but sent after the action's wait
// (a queued job) used to be lost; it stays with its action.
func TestTaggedLateMailStaysWithItsAction(t *testing.T) {
	srv := httptest.NewServer(catcher.NewServer(catcher.NewInbox(100), catcher.Config{Version: "test"}))
	defer srv.Close()
	echo := client.New(srv.URL, "")
	shop := asyncSite(t, echo, map[time.Duration]wire.Capture{0: email("Receipt"), 400 * time.Millisecond: email("Invoice")})
	defer shop.Close()

	cfg := config.Default()
	cfg.Site = shop.URL
	cfg.Quiet, cfg.Timeout = config.Duration{Duration: 2 * time.Second}, config.Duration{Duration: 10 * time.Second}
	cfg.Actions = []config.Action{
		{Name: "Checkout", HTTP: &config.HTTPAction{URL: "/"}, Expect: config.Expect{model.Email: {N: 1}}},
		{Name: "Later", Command: "sleep 1", Quiet: dur(50 * time.Millisecond)},
	}
	run, err := Run(context.Background(), Options{Config: cfg, Client: echo})
	if err != nil {
		t.Fatal(err)
	}
	var subjects []string
	for _, m := range run.Action("action:checkout").Messages {
		subjects = append(subjects, m.Subject)
	}
	if !slices.Equal(subjects, []string{"Receipt", "Invoice"}) {
		t.Errorf("checkout: %q", subjects)
	}
	if len(run.Action("action:later").Messages) != 0 {
		t.Error("the later action must not get the invoice")
	}
	if len(run.Warnings) != 2 || !regexp.MustCompile(`^Checkout: email message \w+ arrived `).MatchString(run.Warnings[0]) {
		t.Errorf("warnings: %q", run.Warnings)
	}
}

func TestAfter(t *testing.T) {
	end := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for at, want := range map[time.Time]string{
		end.Add(1234 * time.Millisecond): "1.2 s",
		end.Add(-time.Second):            "0.0 s", // clocks a little apart
		{}:                               "0.0 s",
	} {
		if got := after(at, end); got != want {
			t.Errorf("after(%s) = %q, want %q", at, got, want)
		}
	}
}

// A late-mail warning names the message without its subject or URL,
// which can hold a one-time code or a token that run warnings would show
// unmasked.
func TestDescribeLeavesContentOut(t *testing.T) {
	for _, c := range []struct {
		m    model.Message
		want string
	}{
		{model.Message{ID: "m1", Channel: model.Email, Subject: "Your code is 123456"}, "email message m1"},
		{model.Message{ID: "m2", Channel: model.Webhook, URL: "https://hooks.test/T0/secret-token"}, "webhook message m2"},
		{model.Message{ID: "m3", Channel: model.Analytics, Event: "purchase"}, `analytics message m3 (event "purchase")`},
		{model.Message{}, "a message"},
	} {
		if got := describe(c.m); got != c.want {
			t.Errorf("describe = %q, want %q", got, c.want)
		}
	}
}
