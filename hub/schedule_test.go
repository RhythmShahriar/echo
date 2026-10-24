// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package hub

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/runner"
	"github.com/RhythmShahriar/echo/wire"
)

func TestParseSchedule(t *testing.T) {
	// Sat 24 Oct 2026, 10:00 UTC.
	now := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		spec, next string // next as "Mon 2 15:04"; "" means a parse error
	}{
		{"Tue 02:00", "Tue 27 02:00"},
		{"tuesday 2:00", "Tue 27 02:00"},
		{"Sat 10:00", "Sat 31 10:00"}, // the same time today is not "after now"
		{"Sat 10:01", "Sat 24 10:01"},
		{"Mon,Thu 03:30", "Mon 26 03:30"},
		{"daily 02:00", "Sun 25 02:00"},
		{"23:59", "Sat 24 23:59"},
		{"weekdays 22:00", "Mon 26 22:00"},
		{"weekends 06:00", "Sun 25 06:00"},
		{"Tue", ""},
		{"Tue 24:00", ""},
		{"Tue 02:60", ""},
		{"Tu 02:00", ""},
		{"Tues 02:00", "Tue 27 02:00"},
		{"Tuxday 02:00", ""},
		{"every Tue 02:00", ""},
		{"", ""},
	} {
		t.Run(tc.spec, func(t *testing.T) {
			s, err := ParseSchedule(tc.spec)
			if tc.next == "" {
				if err == nil {
					t.Errorf("parsed %q", tc.spec)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := s.Next(now).Format("Mon 2 15:04"); got != tc.next {
				t.Errorf("Next = %s, want %s", got, tc.next)
			}
		})
	}
}

func TestScheduleDST(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no time zone data")
	}
	// 8 March 2026: clocks go from 02:00 to 03:00, so 02:30 doesn't exist.
	s, _ := ParseSchedule("daily 02:30")
	got := s.Next(time.Date(2026, 3, 7, 12, 0, 0, 0, ny))
	if want := time.Date(2026, 3, 9, 2, 30, 0, 0, ny); !got.Equal(want) {
		t.Errorf("Next = %s, want %s", got, want)
	}
}

// shop is a site fixture: POST /order emails the buyer through the hub
// with the site's write token, or the owner once the file broken exists
// in dir (which the --after stub creates).
func shop(t *testing.T, addr, token, dir string) {
	t.Helper()
	c := client.New("http://"+hubAddr, token)
	listen(t, addr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		to := "ann@example.com"
		if _, err := os.Stat(filepath.Join(dir, "broken")); err == nil {
			to = "owner@shop.test"
		}
		_, err := c.Capture(r.Context(), wire.Capture{Kind: "email", Run: r.Header.Get(wire.RunHeader), Source: wire.Source{Via: "wordpress"},
			Email: &wire.Email{From: "shop@shop.test", To: []string{to}, Subject: "Order received", Text: "Thanks"}})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))
	cfg := "site: http://" + addr + "\nquiet: 150ms\ntimeout: 5s\nactions:\n  - name: Checkout\n    http:\n      url: /order\n      form: {email: ann@example.com}\n"
	if err := os.MkdirAll(filepath.Join(dir, ".echo"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".echo", "config.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestRunWindow runs a window over two sites. The stub update breaks
// client-a (its shop now emails the owner) and leaves client-b alone (its
// shop ignores the file).
func TestRunWindow(t *testing.T) {
	sitesDir := t.TempDir()
	sites, err := LoadSites(writeSites(t, sitesDir), getenv)
	if err != nil {
		t.Fatal(err)
	}
	store := t.TempDir()
	h, err := New(Options{Store: store, Sites: sites, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = h.Close() }()
	listen(t, hubAddr, h)
	shop(t, shopAAddr, tokens["TOKEN_A"], filepath.Join(sitesDir, "a"))
	shop(t, shopBAddr, tokens["TOKEN_B"], filepath.Join(t.TempDir(), "elsewhere"))
	// client-b's config lives in sitesDir/b: point it at its shop there.
	if err := os.MkdirAll(filepath.Join(sitesDir, "b", ".echo"), 0o750); err != nil {
		t.Fatal(err)
	}
	cfgB := "site: http://" + shopBAddr + "\nquiet: 150ms\ntimeout: 5s\nactions:\n  - name: Checkout\n    http:\n      url: /order\n      form: {email: ann@example.com}\n  - name: Refund\n    replay:\n      event: charge.refunded\n      to: /hook\n"
	var replayed []string
	Replay = func(config.Config) runner.Replayer {
		return func(_ context.Context, a config.ReplayAction, to, _ string) error {
			replayed = append(replayed, a.Event+" "+to)
			return nil
		}
	}
	defer func() { Replay = nil }()
	if err := os.WriteFile(filepath.Join(sitesDir, "b", ".echo", "config.yaml"), []byte(cfgB), 0o600); err != nil {
		t.Fatal(err)
	}

	var log bytes.Buffer
	chosen, _ := sites.Select("all")
	results := RunWindow(context.Background(), WindowOptions{Store: store, Sites: chosen, URL: "http://" + hubAddr, Token: tokens["READ"],
		After: `touch broken && echo "updated $RHYTHM_ECHO_SITE"`, Version: "test", Log: &log})
	if len(results) != 2 {
		t.Fatalf("results: %+v\n%s", results, log.String())
	}
	a, b := results[0], results[1]
	if a.Site != "client-a" || a.Error != "" || a.Counts["broke"] != 1 || a.Counts["new"] != 1 || !a.Broke() {
		t.Errorf("client-a: %+v\n%s", a, log.String())
	}
	if b.Site != "client-b" || b.Error != "" || b.Counts["broke"] != 0 || b.Broke() || !strings.HasPrefix(b.Verdict, "Nothing broke") {
		t.Errorf("client-b: %+v\n%s", b, log.String())
	}
	if len(replayed) != 2 || replayed[0] != "charge.refunded http://"+shopBAddr+"/hook" {
		t.Errorf("replay hook: %q", replayed) // the snapshot and the compare
	}
	if _, err := os.Stat(filepath.Join(sitesDir, "b", "broken")); err != nil {
		t.Errorf("the update didn't run in client-b's folder: %v", err)
	}
	for _, want := range []string{"client-a:   updated client-a", "client-b:   updated client-b", "client-a: Something broke"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("log has no %q:\n%s", want, log.String())
		}
	}
	for _, r := range results {
		for _, f := range []string{"run.json", "comparison.json", "schedule.json"} {
			if _, err := os.Stat(filepath.Join(r.Dir, f)); err != nil {
				t.Errorf("%s: %v", r.Site, err)
			}
		}
	}
	hist, err := History(store, "client-a", time.Now().Add(-time.Hour))
	if err != nil || len(hist) != 1 || hist[0].Counts["broke"] != 1 {
		t.Errorf("history: %+v %v", hist, err)
	}
	if hist, _ := History(store, "client-a", time.Now().Add(time.Hour)); len(hist) != 0 {
		t.Errorf("history since later: %+v", hist)
	}

	// A failing update is recorded and the compare still runs.
	results = RunWindow(context.Background(), WindowOptions{Store: store, Sites: chosen[1:], URL: "http://" + hubAddr, Token: tokens["READ"],
		After: "echo nope >&2; exit 3", Snapshot: "pre-update", Version: "test", Log: &log})
	if len(results) != 1 || !strings.Contains(results[0].AfterError, "exit status 3") || results[0].Dir == "" || !results[0].Broke() {
		t.Errorf("failed update: %+v", results)
	}
	// A snapshot that isn't there is a site error, not a stop.
	results = RunWindow(context.Background(), WindowOptions{Store: store, Sites: chosen, URL: "http://" + hubAddr, Token: tokens["READ"],
		Snapshot: "missing", Log: &log})
	if len(results) != 2 || results[0].Error == "" || results[1].Error == "" {
		t.Errorf("missing snapshot: %+v", results)
	}
}

// TestScheduleMain checks the command line and that --once waits for the
// next window with an injected clock.
func TestScheduleMain(t *testing.T) {
	dir := t.TempDir()
	sitesFile := writeSites(t, dir)
	t.Setenv("READ", tokens["READ"])
	t.Setenv("TOKEN_A", tokens["TOKEN_A"])
	t.Setenv("TOKEN_B", tokens["TOKEN_B"])
	now := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC) // a Saturday
	clock := func() time.Time { return now }
	var waited time.Duration
	wait := func(_ context.Context, d time.Duration) error { waited = d; now = now.Add(d); return nil }
	run := func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := scheduleMain(context.Background(), args, &out, &errb, clock, wait)
		return code, out.String(), errb.String()
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"Tue 02:00", "--after", "true", "--file", sitesFile}, "--store is required"},
		{[]string{"Tue 02:00", "--store", dir, "--file", sitesFile}, "--after is required"},
		{[]string{"--store", dir, "--after", "true", "--file", sitesFile}, "say when"},
		{[]string{"Tue", "02:00", "--store", dir, "--after", "true", "--file", sitesFile}, "one schedule, in quotes"},
		{[]string{"Tue 25:00", "--store", dir, "--after", "true", "--file", sitesFile}, `schedule "Tue 25:00"`},
		{[]string{"--now", "--once", "--store", dir, "--after", "true", "--file", sitesFile}, "pick --now or --once"},
		{[]string{"--now", "--store", dir, "--after", "true", "--file", sitesFile, "--sites", "client-z"}, `no site "client-z"`},
		{[]string{"--now", "--store", dir, "--after", "true", "--file", filepath.Join(dir, "none.yaml")}, "none.yaml"},
	} {
		if code, _, errb := run(tc.args...); code != 2 || !strings.Contains(errb, tc.want) {
			t.Errorf("%q: %d %q, want 2 and %q", tc.args, code, errb, tc.want)
		}
	}
	// No hub is listening: each site fails to compare and --once exits 1
	// after waiting until Tuesday 02:00.
	code, out, errb := run("Tue 02:00", "--once", "--store", dir, "--after", "true", "--file", sitesFile, "--url", "http://127.0.0.1:20519")
	if code != 1 || waited != 64*time.Hour {
		t.Errorf("--once: code %d, waited %s\n%s%s", code, waited, out, errb)
	}
	if !strings.Contains(errb, "Next window: Tue 27 Oct 02:00 UTC (in 64h0m0s)") || !strings.Contains(out, "client-a: couldn't compare") {
		t.Errorf("output:\n%s%s", out, errb)
	}
}
