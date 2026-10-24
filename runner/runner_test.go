// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/compare"
	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/wire"
)

// site acts like a WordPress site with Echo's connector: the contact form
// handler emails the admin and the submitter and posts to Zapier; when
// broken, the admin email is lost (a mail plugin update gone wrong).
func site(t *testing.T, echo *client.Client, broken *atomic.Bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		run := r.Header.Get(wire.RunHeader)
		_ = r.ParseForm()
		send := func(c wire.Capture) {
			c.Run, c.Source = run, wire.Source{Via: "wordpress", Host: r.Host}
			if _, err := echo.Capture(context.Background(), c); err != nil {
				t.Error(err)
			}
		}
		if !broken.Load() {
			send(wire.Capture{Kind: "email", Email: &wire.Email{From: "wordpress@shop.test", To: []string{"admin@shop.test"},
				Subject: "New message from " + r.Form.Get("name"), Text: "Message: " + r.Form.Get("message")}})
		}
		send(wire.Capture{Kind: "email", Email: &wire.Email{From: "wordpress@shop.test", To: []string{r.Form.Get("email")},
			Subject: "Thanks for your message", Text: "We'll get back to you."}})
		go func() { // async, like Action Scheduler: arrives after the response
			time.Sleep(200 * time.Millisecond)
			send(wire.Capture{Kind: "http", Phase: wire.Before, HTTP: &wire.HTTP{Method: "POST",
				URL: "https://hooks.zapier.com/hooks/catch/1/x/", Headers: map[string]string{"Content-Type": "application/json"},
				Body: `{"name":"x","email":"y","message":"z"}`}})
		}()
		fmt.Fprint(w, "ok")
	}))
}

func TestSnapshotThenCompare(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("no curl")
	}
	srv := httptest.NewServer(catcher.NewServer(catcher.NewInbox(1000), catcher.Config{Version: "test"}))
	defer srv.Close()
	echo := client.New(srv.URL, "")
	var broken atomic.Bool
	shop := site(t, echo, &broken)
	defer shop.Close()

	cfg := config.Default()
	cfg.Site = shop.URL
	cfg.Quiet = config.Duration{Duration: 500 * time.Millisecond}
	cfg.Timeout = config.Duration{Duration: 2 * time.Second} // the broken run waits it out for the lost email
	cfg.Actions = []config.Action{
		{Name: "Contact form", HTTP: &config.HTTPAction{URL: "/contact", Form: map[string]string{
			"name": "${fake:name}", "email": "${fake:email}", "message": "Hello"}}, Expect: config.Expect{model.Email: {N: 2}}},
		{Name: "Nightly report", Command: `curl -s -X POST ` + srv.URL + `/v1/capture -d "{\"v\":1,\"kind\":\"email\",\"run\":\"$RHYTHM_ECHO_RUN\",\"source\":{\"via\":\"test\"},\"email\":{\"to\":[\"ops@shop.test\"],\"subject\":\"Nightly report\",\"text\":\"All good\"}}" >/dev/null`},
	}
	ctx := context.Background()
	before, err := Run(ctx, Options{Config: cfg, Client: echo, Version: "test", Name: "good"})
	if err != nil {
		t.Fatal(err)
	}
	form := before.Action("action:contact-form")
	if form == nil || len(form.Messages) != 3 || form.Error != "" {
		t.Fatalf("contact form: %+v", form)
	}
	if n := before.Action("action:nightly-report"); n == nil || len(n.Messages) != 1 {
		t.Fatalf("command action: %+v", n)
	}
	for _, a := range before.Actions {
		for _, i := range a.Issues {
			t.Errorf("good state has an issue: %s", i.Title)
		}
	}

	broken.Store(true)
	after, err := Run(ctx, Options{Config: cfg, Client: echo, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	c := compare.Compare(before, after, "", "", "test")
	var got []string
	for _, ch := range c.Changes {
		got = append(got, ch.Class+": "+ch.Title)
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{"broke: Email to admin@shop.test is no longer sent", "broke: Sent fewer emails than expected"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	if c.Count(model.New) != 0 || c.Count(model.Changed) != 0 {
		t.Errorf("only the lost email should change:\n%s", all)
	}
}

func TestProductionRefused(t *testing.T) {
	srv := httptest.NewServer(catcher.NewServer(catcher.NewInbox(10), catcher.Config{}))
	defer srv.Close()
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer target.Close()
	cfg := config.Default()
	cfg.Production = []string{"shop.example"}
	cfg.Quiet, cfg.Timeout = config.Duration{Duration: 50 * time.Millisecond}, config.Duration{Duration: time.Second}

	// The site itself: nothing runs, not even the health check.
	cfg.Site = "HTTPS://WWW.Shop.Example:443/"
	cfg.Actions = []config.Action{{Name: "Order", HTTP: &config.HTTPAction{URL: "/checkout"}}}
	_, err := Run(context.Background(), Options{Config: cfg, Client: client.New("http://127.0.0.1:1", "")})
	if !errors.Is(err, ErrProduction) || err.Error() !=
		`refusing to run against HTTPS://WWW.Shop.Example:443/: it matches "shop.example" under production; actions only run against staging and local sites` {
		t.Errorf("production site: %v", err)
	}

	// An action or replay aimed at production from a staging site.
	cfg.Site = target.URL
	replayed := false
	cfg.Actions = []config.Action{
		{Name: "Order", HTTP: &config.HTTPAction{URL: "http://shop.example/checkout"}},
		{Name: "Paid", Replay: &config.ReplayAction{Event: "payment_intent.succeeded", To: "https://api.shop.example/hook"}},
		{Name: "Local", HTTP: &config.HTTPAction{URL: "/ok"}},
	}
	run, err := Run(context.Background(), Options{Config: cfg, Client: client.New(srv.URL, ""),
		Replay: func(context.Context, config.ReplayAction, string, string) error { replayed = true; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{
		`refusing to run against http://shop.example/checkout: it matches "shop.example" under production`,
		`refusing to run against https://api.shop.example/hook: it matches "shop.example" under production`,
		"",
	} {
		if run.Actions[i].Error != want {
			t.Errorf("%s: error = %q, want %q", run.Actions[i].Label, run.Actions[i].Error, want)
		}
	}
	if replayed || hits.Load() != 1 {
		t.Errorf("replayed %v, %d requests to the staging site", replayed, hits.Load())
	}
}

func TestSelect(t *testing.T) {
	if _, err := Select(config.Config{}, nil); !errors.Is(err, ErrNoActions) {
		t.Errorf("no actions: %v", err)
	}
	cfg := config.Config{Actions: []config.Action{{Name: "Contact form"}, {Name: "Checkout"}, {Name: "Reset"}}}
	got, err := Select(cfg, []string{"reset", "contact FORM"})
	if err != nil || len(got) != 2 || got[0].Name != "Contact form" || got[1].Name != "Reset" {
		t.Errorf("Select = %v, %v", got, err)
	}
	if _, err := Select(cfg, []string{"Checkout", "nope"}); !errors.Is(err, ErrUnknownAction) || !strings.Contains(err.Error(), `"nope"`) ||
		!strings.Contains(err.Error(), "Contact form, Checkout, Reset") {
		t.Errorf("unknown action: %v", err)
	}
}

func TestExpectationsCoverEveryChannel(t *testing.T) {
	msgs := []model.Message{{Channel: model.Email}, {Channel: model.Analytics, Event: "purchase"}}
	e := config.Expect{model.Email: {N: 1}, model.Analytics: {Events: map[string]int{"purchase": 1, "add_to_cart": 1}}, model.Push: {N: 1}}
	var got []string
	for _, i := range expectations(e, msgs) {
		got = append(got, i.Check+": "+i.Title+" ("+i.Detail+")")
	}
	want := "expect/analytics: Sent fewer analytics events than expected (expected 2, got 1)\n" +
		"expect/push: Sent fewer push notifications than expected (expected 1, got 0)"
	if strings.Join(got, "\n") != want {
		t.Errorf("got:\n%s", strings.Join(got, "\n"))
	}
	if len(expectations(nil, msgs)) != 0 {
		t.Error("no expect, no issues")
	}
}

// TestReplayOnlySealed: a replay action under observe would make the
// site send real order emails, so it's refused before anything is sent.
func TestReplayOnlySealed(t *testing.T) {
	for _, mode := range []string{catcher.Observe, catcher.Seal} {
		srv := httptest.NewServer(catcher.NewServer(catcher.NewInbox(10), catcher.Config{Mode: mode}))
		var hits atomic.Int32
		target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
		cfg := config.Default()
		cfg.Site = target.URL
		cfg.Quiet, cfg.Timeout = config.Duration{Duration: 50 * time.Millisecond}, config.Duration{Duration: time.Second}
		cfg.Actions = []config.Action{{Name: "Paid", Replay: &config.ReplayAction{Event: "payment_intent.succeeded", To: "/hook"}}}
		replay := func(ctx context.Context, _ config.ReplayAction, to, _ string) error {
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, to, nil)
			resp, err := http.DefaultClient.Do(req)
			if err == nil {
				_ = resp.Body.Close()
			}
			return err
		}
		run, err := Run(context.Background(), Options{Config: cfg, Client: client.New(srv.URL, ""), Replay: replay})
		srv.Close()
		target.Close()
		if err != nil {
			t.Fatal(err)
		}
		got := run.Actions[0]
		switch mode {
		case catcher.Observe:
			want := "Echo is in observe mode; replays only run sealed, so the site's reactions can't reach anyone (rhythm echo up --seal)"
			if got.Error != want || hits.Load() != 0 || got.Kind != "replay" {
				t.Errorf("observe: error %q, %d requests", got.Error, hits.Load())
			}
		case catcher.Seal:
			if got.Error != "" || hits.Load() != 1 {
				t.Errorf("seal: error %q, %d requests", got.Error, hits.Load())
			}
		}
	}
}

// capture is a transport that keeps the request body instead of sending it.
type capture struct{ body []byte }

func (c *capture) RoundTrip(r *http.Request) (*http.Response, error) {
	c.body, _ = io.ReadAll(r.Body)
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
}

// TestJSONBodyPlaceholders: placeholders in a JSON body are filled before
// it's encoded, so any value gives valid JSON.
func TestJSONBodyPlaceholders(t *testing.T) {
	values := map[string]string{
		"ZZ_QUOTE":     `O"Brien \ Ltd`,
		"ZZ_NEWLINE":   "line one\nline two\r\n\ttabbed",
		"ZZ_UNICODE":   "Zoë 東京 \u202eevil \U0001F600 \u2028",
		"ZZ_BRACES":    `}],"x":{`,
		"ZZ_KEY":       `k"ey\`,
		"ZZ_EMPTY_NOT": "",
	}
	for k, v := range values {
		t.Setenv(k, v)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	yml := `site: http://shop.test
actions:
  - name: Order
    http:
      url: /order
      json:
        name: "${env:ZZ_QUOTE}"
        note: "${env:ZZ_NEWLINE}"
        city: "Hi ${env:ZZ_UNICODE}!"
        "${env:ZZ_KEY}": "${env:ZZ_BRACES}"
        lines: ["${env:ZZ_QUOTE}", 2, true, null, {sku: "${fake:tag}"}]
        empty: "${env:ZZ_EMPTY_NOT}"
        1: one
        count: 3
`
	if err := os.WriteFile(path, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var tr capture
	fake := &fakeData{tag: "abc123"}
	o := Options{Config: cfg, HTTP: &http.Client{Transport: &tr}}
	if err := doHTTP(context.Background(), o, cfg.Actions[0], "run", fake); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(tr.body) {
		t.Fatalf("invalid JSON: %s", tr.body)
	}
	var got map[string]any
	if err := json.Unmarshal(tr.body, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"name":           values["ZZ_QUOTE"],
		"note":           values["ZZ_NEWLINE"],
		"city":           "Hi " + values["ZZ_UNICODE"] + "!",
		values["ZZ_KEY"]: values["ZZ_BRACES"],
		"lines":          []any{values["ZZ_QUOTE"], 2.0, true, nil, map[string]any{"sku": "abc123"}},
		"empty":          "",
		"1":              "one",
		"count":          3.0,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("body = %#v\nwant   %#v", got, want)
	}
}

func TestExpandJSONLeavesOtherValues(t *testing.T) {
	f := &fakeData{tag: "t"}
	for _, v := range []any{nil, 3, 2.5, true, "plain", "${fake:unknown}"} {
		if got := f.expandJSON(v); got != v {
			t.Errorf("expandJSON(%#v) = %#v", v, got)
		}
	}
	if got := f.expandJSON("${fake:tag}"); got != "t" {
		t.Errorf("a top-level string: %#v", got)
	}
}

// A Config built in code skips Load's checks; a timeout of 0 must not
// fail every action or leave the HTTP client waiting forever.
func TestZeroWaitsUseDefaults(t *testing.T) {
	c := withWaits(config.Config{})
	if d := config.Default(); c.Timeout != d.Timeout || c.Quiet != d.Quiet {
		t.Errorf("withWaits: %+v", c)
	}
	if c := withWaits(config.Config{Timeout: config.Duration{Duration: -time.Second}, Quiet: config.Duration{Duration: time.Second}}); c.Quiet.Duration != time.Second || c.Timeout.Duration <= 0 {
		t.Errorf("withWaits kept a set quiet or a negative timeout: %+v", c)
	}
	srv := httptest.NewServer(catcher.NewServer(catcher.NewInbox(10), catcher.Config{}))
	defer srv.Close()
	site := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer site.Close()
	cfg := config.Default()
	cfg.Site, cfg.Timeout, cfg.Quiet = site.URL, config.Duration{}, config.Duration{Duration: 50 * time.Millisecond}
	cfg.Actions = []config.Action{{Name: "Ping", HTTP: &config.HTTPAction{URL: "/"}}}
	run, err := Run(context.Background(), Options{Config: cfg, Client: client.New(srv.URL, "")})
	if err != nil || run.Actions[0].Error != "" {
		t.Errorf("timeout 0: %v %+v", err, run)
	}
}
