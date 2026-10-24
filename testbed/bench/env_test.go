// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-smtp"

	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/store"
)

// TestEnvironmentManifest checks what the v1.0.1 scenarios must test, as
// the manifest states it: the site down at both ends (exit 3, never all
// clear), two compares at once (no foreign mail, a warning for the
// untagged SMTP only) and a token-protected Echo on 0.0.0.0 (live view
// and API both need it). Every environment scenario has a probe, says
// what its broken state is and is listed in the scoreboard.
func TestEnvironmentManifest(t *testing.T) {
	_, m := testbedDir(t)
	env := map[string]EnvScenario{}
	for _, s := range m.Environment {
		env[s.ID] = s
	}
	has := func(id, step string, ok func(Check) bool) {
		t.Helper()
		s, found := env[id]
		if !found {
			t.Fatalf("no %s in the manifest", id)
		}
		if !slices.ContainsFunc(s.Broken, func(c Check) bool { return c.Step == step && ok(c) }) {
			t.Errorf("%s: the broken state's %q step doesn't check what it should: %+v", id, step, s.Broken)
		}
	}
	exit := func(code int) func(Check) bool { return func(c Check) bool { return c.Exit != nil && *c.Exit == code } }
	says := func(text string) func(Check) bool { return func(c Check) bool { return slices.Contains(c.Says, text) } }
	never := func(text string) func(Check) bool {
		return func(c Check) bool { return slices.Contains(c.Never, text) }
	}
	has("e01", "snapshot", exit(3))
	has("e01", "compare", exit(3))
	has("e01", "compare", never("all clear"))
	has("e02", "compare a", never("foreign mail"))
	has("e02", "compare b", never("foreign mail"))
	has("e02", "compare b", says("1 message could not be tied"))
	has("e03", "requests", says("live view without the token: 401"))
	has("e03", "requests", says("API without the token: 401"))
	has("e03", "up without a token", exit(2))

	ids := make([]string, 0, len(m.Environment))
	for i, s := range m.Environment {
		ids = append(ids, s.ID)
		if want := fmt.Sprintf("e%02d", i+1); s.ID != want {
			t.Errorf("environment scenario %d is %s, want %s", i+1, s.ID, want)
		}
	}
	board := scoreboard(m, &Run{Env: func() []EnvOutcome {
		var out []EnvOutcome
		for _, s := range m.Environment {
			out = append(out, EnvOutcome{Scenario: s, Echo: Result{Outcome: Caught}})
		}
		return out
	}()})
	for _, id := range ids {
		if !strings.Contains(board, "| "+id+" |") {
			t.Errorf("the scoreboard has no row for %s", id)
		}
	}
	if !strings.Contains(board, "15 of 15 environment scenarios") {
		t.Errorf("the scoreboard's summary:\n%s", board[:min(len(board), 1500)])
	}
}

func intp(i int) *int { return &i }

// TestVerify covers each kind of check, passing and failing.
func TestVerify(t *testing.T) {
	broke := model.Change{Class: model.Broke, Check: "message/missing", Title: "Email to ann@shop.test is no longer sent"}
	obs := []Observation{{Step: "compare", Exit: 3, Verdict: "error", Output: "Every action failed\nnote: 1 message could not be tied", Changes: []model.Change{broke}}}
	for _, c := range []struct {
		check Check
		fails string
	}{
		{Check{Step: "compare", Exit: intp(3), Verdict: "error", Says: []string{"every action FAILED"}, Never: []string{"all clear"}}, ""},
		{Check{Step: "compare", Change: &Expect{Class: model.Broke, Check: "message/missing", Title: "ann@shop.test"}}, ""},
		{Check{Step: "compare", NoClass: []string{model.Changed}}, ""},
		{Check{Step: "compare", Exit: intp(0)}, "exit 3, want 0"},
		{Check{Step: "compare", Verdict: "pass"}, `verdict "error", want "pass"`},
		{Check{Step: "compare", Says: []string{"restarted"}}, `doesn't say "restarted"`},
		{Check{Step: "compare", Never: []string{"could not be tied"}}, `says "could not be tied"`},
		{Check{Step: "compare", Change: &Expect{Class: model.New, Check: "message/new"}}, "no NEW message/new"},
		{Check{Step: "compare", NoClass: []string{model.Broke}}, "reports BROKE message/missing"},
		{Check{Step: "snapshot"}, "the probe has no such step"},
	} {
		got := Verify([]Check{c.check}, obs)
		if c.fails == "" && got != "" || c.fails != "" && !strings.Contains(got, c.fails) {
			t.Errorf("%s: Verify = %q, want %q", c.check, got, c.fails)
		}
	}
}

// TestEvaluateEnv: a good state that fails its own checks is an error,
// a broken state that fails one is a miss and passing both is caught.
func TestEvaluateEnv(t *testing.T) {
	s := EnvScenario{ID: "e99", Good: []Check{{Step: "run", Exit: intp(0)}}, Broken: []Check{{Step: "run", Exit: intp(3)}}}
	ok := []Observation{{Step: "run", Exit: 0}}
	down := []Observation{{Step: "run", Exit: 3}}
	if r := EvaluateEnv(s, ok, down); r.Outcome != Caught || r.Found != "run: exit 3" {
		t.Errorf("caught: %+v", r)
	}
	if r := EvaluateEnv(s, ok, ok); r.Outcome != Missed || r.Detail != "run: exit 0, want 3" {
		t.Errorf("missed: %+v", r)
	}
	if r := EvaluateEnv(s, down, down); r.Outcome != Failed || !strings.HasPrefix(r.Detail, "good state: ") {
		t.Errorf("good state failing: %+v", r)
	}
	p := &ProbeEnv{Work: t.TempDir()}
	if r := runEnv(context.Background(), p, EnvScenario{ID: "e98", Probe: "colliding-names", Needs: "testbed"}); r.Echo.Outcome != NotRun {
		t.Errorf("a testbed scenario without the testbed: %+v", r.Echo)
	}
	if r := runEnv(context.Background(), p, EnvScenario{ID: "e97", Probe: "windows", Needs: "windows"}); r.Echo.Outcome != NotRun {
		t.Errorf("a Windows scenario elsewhere: %+v", r.Echo)
	}
}

func TestPick(t *testing.T) {
	_, m := testbedDir(t)
	sites, env, err := m.Pick([]string{"s05", "e03"})
	if err != nil || len(sites) != 1 || sites[0].ID != "s05" || len(env) != 1 || env[0].ID != "e03" {
		t.Errorf("Pick(s05, e03) = %v %v %v", sites, env, err)
	}
	if sites, env, _ := m.Pick([]string{"env"}); len(sites) != 0 || len(env) != len(m.Environment) {
		t.Errorf("Pick(env) = %d sites, %d environment", len(sites), len(env))
	}
	if _, _, err := m.Pick([]string{"e99"}); err == nil {
		t.Error("Pick(e99) found it")
	}
}

func TestParseTools(t *testing.T) {
	for spec, want := range map[string]string{
		"auto": "mailpit buggregator staging-shield", "": "mailpit buggregator staging-shield", "none": "",
		"echo mailpit buggregator staging-shield": "mailpit buggregator staging-shield",
		"staging-shield,mailpit":                  "mailpit staging-shield",
		"echo":                                    "",
	} {
		names, _, err := parseTools(spec)
		if err != nil || strings.Join(names, " ") != want {
			t.Errorf("parseTools(%q) = %v %v, want %q", spec, names, err, want)
		}
	}
	if _, required, _ := parseTools("mailpit"); !required {
		t.Error("a named tool isn't required")
	}
	if _, _, err := parseTools("echo mailtrap"); err == nil {
		t.Error("an unknown tool was accepted")
	}
}

// TestOldSnapshotsLoad: each released schema's snapshot fixture loads
// with this Echo's store, as e11 needs.
func TestOldSnapshotsLoad(t *testing.T) {
	dir, _ := testbedDir(t)
	fixtures, err := filepath.Glob(filepath.Join(dir, "bench", "testdata", "runs", "*", "run.json"))
	if err != nil || len(fixtures) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	for _, f := range fixtures {
		r, err := store.LoadRun(filepath.Dir(f))
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		if r.Version != filepath.Base(filepath.Dir(f)) || len(r.Actions) != 1 || len(r.Actions[0].Messages) != 1 {
			t.Errorf("%s: version %s, %d actions", f, r.Version, len(r.Actions))
		}
	}
}

// TestDiffResults: two runs with the same outcomes match whatever their
// timings; a changed outcome is listed.
func TestDiffResults(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, r Results) string {
		data, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	run := func(echo, mp, env string, secs float64) Results {
		return Results{
			Sites:       []Outcome{{Scenario: Scenario{ID: "s01"}, Echo: Result{Outcome: echo}, Tools: map[string]Result{"mailpit": {Outcome: mp}}, Seconds: secs}},
			Environment: []EnvOutcome{{Scenario: EnvScenario{ID: "e01"}, Echo: Result{Outcome: env, Detail: fmt.Sprint(secs)}}},
		}
	}
	a := write("a.json", run(Caught, Missed, Caught, 1))
	b := write("b.json", run(Caught, Missed, Caught, 2))
	c := write("c.json", run(Caught, Missed, Missed, 2))
	var out bytes.Buffer
	if same, err := diffResults(a, b, &out); err != nil || !same {
		t.Errorf("a, b: %v %v %s", same, err, out.String())
	}
	out.Reset()
	if same, err := diffResults(a, c, &out); err != nil || same || out.String() != "e01: echo caught | echo missed\n" {
		t.Errorf("a, c: %v %v %q", same, err, out.String())
	}
}

// stubSMTP is an SMTP server that keeps what it's sent.
type stubSMTP struct {
	mu   sync.Mutex
	msgs []*mail.Message
	body []string
}

func (s *stubSMTP) NewSession(*smtp.Conn) (smtp.Session, error) { return &stubSession{s: s}, nil }

type stubSession struct{ s *stubSMTP }

func (*stubSession) Mail(string, *smtp.MailOptions) error { return nil }
func (*stubSession) Rcpt(string, *smtp.RcptOptions) error { return nil }
func (*stubSession) Reset()                               {}
func (*stubSession) Logout() error                        { return nil }
func (x *stubSession) Data(r io.Reader) error {
	raw, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	body, _ := io.ReadAll(m.Body)
	x.s.mu.Lock()
	defer x.s.mu.Unlock()
	x.s.msgs, x.s.body = append(x.s.msgs, m), append(x.s.body, string(body))
	return nil
}

func startSMTP(t *testing.T) (*stubSMTP, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	b := &stubSMTP{}
	srv := smtp.NewServer(b)
	srv.Domain, srv.AllowInsecureAuth = "localhost", true
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return b, ln.Addr().String()
}

// runs are a good and a broken run of a scenario: the broken one sends
// the receipt to the old agency's address.
func runs(to string) (*model.Run, *model.Run) {
	msg := func(to string) model.Message {
		return model.Message{Channel: model.Email, From: "shop@shop.example.com", To: []string{to}, Subject: "New message", Text: "A visitor wrote.", At: time.Now()}
	}
	good := &model.Run{Actions: []model.Action{{Messages: []model.Message{msg("owner@shop.example.com")}}}}
	broken := &model.Run{Actions: []model.Action{{Messages: []model.Message{msg(to)}}}}
	return good, broken
}

var wrongAddress = Scenario{ID: "s09", Channel: "email", Area: "content", Mailpit: "webmaster@old-agency.example.net"}

// TestMailpitAgainstAStub runs the Mailpit adapter against a stand-in
// with Mailpit's API: tags from X-Tags, search and message by ID.
func TestMailpitAgainstAStub(t *testing.T) {
	sink, addr := startSMTP(t)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sink.mu.Lock()
		defer sink.mu.Unlock()
		switch {
		case r.URL.Path == "/api/v1/info":
			_, _ = io.WriteString(w, `{"Version":"stub"}`)
		case r.URL.Path == "/api/v1/search":
			tag := strings.TrimSuffix(strings.TrimPrefix(r.URL.Query().Get("query"), `tag:"`), `"`)
			var ids []map[string]string
			for i, m := range sink.msgs {
				if m.Header.Get("X-Tags") == tag {
					ids = append(ids, map[string]string{"ID": fmt.Sprint(i)})
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"messages": ids, "messages_count": len(ids)})
		case strings.HasPrefix(r.URL.Path, "/api/v1/message/"):
			var i int
			_, _ = fmt.Sscan(strings.TrimPrefix(r.URL.Path, "/api/v1/message/"), &i)
			m := sink.msgs[i]
			_ = json.NewEncoder(w).Encode(map[string]any{"Subject": m.Header.Get("Subject"), "From": map[string]string{"Address": m.Header.Get("From")},
				"To": []map[string]string{{"Address": m.Header.Get("To")}}, "Text": sink.body[i]})
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	tool := mailpit{api: api.URL, smtp: addr}
	if err := tool.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	good, broken := runs("webmaster@old-agency.example.net")
	if r := tool.Judge(context.Background(), wrongAddress, good, broken, "1"); r.Outcome != Caught {
		t.Errorf("wrong address: %+v", r)
	}
	good, broken = runs("owner@shop.example.com")
	broken.Actions[0].Messages = nil
	if r := tool.Judge(context.Background(), Scenario{ID: "s01", Channel: "email"}, good, broken, "2"); r.Outcome != Missed || !strings.Contains(r.Detail, "instead of 1") {
		t.Errorf("lost email: %+v", r)
	}
	if r := tool.Judge(context.Background(), Scenario{ID: "s03", Channel: "sms"}, good, broken, "3"); r.Outcome != CantSee {
		t.Errorf("SMS: %+v", r)
	}
	if err := (mailpit{api: "http://127.0.0.1:1"}).Ready(context.Background()); err == nil {
		t.Error("Ready with nothing there")
	}
}

// TestBuggregatorAgainstAStub runs the Buggregator adapter against a
// stand-in with Buggregator's events API: GET and DELETE /api/events.
func TestBuggregatorAgainstAStub(t *testing.T) {
	sink, addr := startSMTP(t)
	cleared := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sink.mu.Lock()
		defer sink.mu.Unlock()
		if r.URL.Path != "/api/events" {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodDelete {
			body, _ := io.ReadAll(r.Body)
			if string(body) != `{"type":"smtp"}` {
				http.Error(w, "bad body", http.StatusBadRequest)
				return
			}
			sink.msgs, sink.body = nil, nil
			cleared++
			return
		}
		var data []map[string]any
		for i, m := range sink.msgs {
			data = append(data, map[string]any{"uuid": fmt.Sprint(i), "type": "smtp", "payload": map[string]any{
				"subject": m.Header.Get("Subject"), "from": []map[string]string{{"email": m.Header.Get("From"), "name": ""}},
				"to": []map[string]string{{"email": m.Header.Get("To")}}, "text": sink.body[i], "html": ""}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer api.Close()
	tool := buggregator{api: api.URL, smtp: addr}
	if err := tool.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	good, broken := runs("webmaster@old-agency.example.net")
	if r := tool.Judge(context.Background(), wrongAddress, good, broken, "1"); r.Outcome != Caught {
		t.Errorf("wrong address: %+v", r)
	}
	if cleared != 2 {
		t.Errorf("cleared %d times, want once per state", cleared)
	}
	good, broken = runs("owner@shop.example.com")
	if r := tool.Judge(context.Background(), wrongAddress, good, broken, "2"); r.Outcome != Missed {
		t.Errorf("nothing visible: %+v", r)
	}
}

// TestStagingShieldAgainstAStub runs the Staging Shield adapter with
// WP-CLI, Echo and Mailpit stood in: the plugin is installed, activated
// for the staging scenario only and counted caught when the email to the
// real address didn't reach the mail server.
func TestStagingShieldAgainstAStub(t *testing.T) {
	var mu sync.Mutex
	sent, active, leaks := 0, false, false
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Query().Get("query") != `to:"jamie.rivera@gmail.com"` {
			http.Error(w, "unexpected query "+r.URL.RawQuery, http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]int{"messages_count": sent})
	}))
	defer api.Close()
	dir, _ := testbedDir(t)
	tool := stagingShield{testbed: dir, mail: mailpit{api: api.URL}, work: t.TempDir(),
		run: func(_ context.Context, _ string, args ...string) (int, string) {
			mu.Lock()
			defer mu.Unlock()
			switch strings.Join(args[1:], " ") {
			case "plugin get staging-shield --field=version":
				return 0, "1.0.2\n"
			case "plugin activate staging-shield":
				active = true
			case "plugin deactivate staging-shield":
				active = false
			default:
				return 1, "unexpected wp " + strings.Join(args[1:], " ")
			}
			return 0, ""
		},
		start: func(_ context.Context, mode string) error {
			if mode != "observe" {
				return fmt.Errorf("mode %s", mode)
			}
			return nil
		},
		action: func(context.Context, string, string) error {
			mu.Lock()
			defer mu.Unlock()
			if !active {
				return fmt.Errorf("the plugin isn't active")
			}
			if leaks {
				sent++
			}
			return nil
		}}
	if err := tool.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r := tool.Judge(context.Background(), Scenario{ID: "s01", Area: "lost", Channel: "email"}, nil, nil, ""); r.Outcome != CantSee {
		t.Errorf("a lost email: %+v", r)
	}
	s19 := Scenario{ID: "s19", Area: "staging", Mode: "observe", Action: "Resend the receipt for order 1000", Channel: "email", Mailpit: "jamie.rivera@gmail.com"}
	if r := tool.Judge(context.Background(), s19, nil, nil, ""); r.Outcome != Caught {
		t.Errorf("stopped: %+v", r)
	}
	if active {
		t.Error("the plugin was left active")
	}
	leaks = true
	if r := tool.Judge(context.Background(), s19, nil, nil, ""); r.Outcome != Missed || !strings.Contains(r.Detail, "still reached") {
		t.Errorf("leaked: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(dir, "state", "s19")); err == nil {
		t.Error("s19 was left broken")
	}
}

// TestLocalProbes runs every environment scenario that needs nothing but
// Echo against the binary built from this checkout, on
// 127.0.0.1:23200-23239 and wants each caught. The Windows one runs too,
// as if on Windows: its probe uses nothing platform-specific, so this
// checks it before CI's go-windows job does.
func TestLocalProbes(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and starts Echo for each scenario")
	}
	dir, m := testbedDir(t)
	p := &ProbeEnv{Bin: buildEcho(t, dir), Root: filepath.Dir(dir), Testbed: dir, Work: t.TempDir(), Timeout: time.Minute, Windows: true}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	for _, s := range m.Environment {
		if s.Needs == "testbed" {
			continue
		}
		t.Run(s.ID, func(t *testing.T) {
			out := runEnv(ctx, p, s)
			if out.Echo.Outcome != Caught {
				t.Errorf("%s %s: %s %s\ngood: %+v\nbroken: %+v", s.ID, s.Name, out.Echo.Outcome, out.Echo.Detail, out.Good, out.Broken)
			}
		})
	}
}
