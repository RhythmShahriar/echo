// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/wire"
)

// vector is one testdata/*.json capture and decision pair.
type vector struct {
	Name      string          `json:"name"`
	Valid     bool            `json:"valid"`
	Problems  []string        `json:"problems"`
	Mode      string          `json:"mode"`
	Echo      string          `json:"echo"`
	Capture   json.RawMessage `json:"capture"`
	Decision  map[string]any  `json:"decision"`
	Connector string          `json:"connector"`
}

func vectors(t *testing.T) map[string]vector {
	t.Helper()
	files, err := filepath.Glob("testdata/*.json")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]vector{}
	for _, f := range files {
		if filepath.Base(f) == "run-signature.json" {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var v vector
		if err := json.Unmarshal(b, &v); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		out[filepath.Base(f)] = v
	}
	if len(out) < 10 {
		t.Fatalf("only %d vectors", len(out))
	}
	return out
}

func schema(t *testing.T) *Schema {
	t.Helper()
	s, err := WireSchema()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Each vector's capture is valid or broken as it says, with the problems it
// names. Echo's real catcher answers the valid ones as the vector says.
func TestVectors(t *testing.T) {
	sc := schema(t)
	for file, v := range vectors(t) {
		t.Run(file, func(t *testing.T) {
			problems := sc.Capture(v.Capture)
			if v.Valid != (len(problems) == 0) {
				t.Fatalf("valid = %v, problems %q", v.Valid, problems)
			}
			for _, want := range v.Problems {
				if !strings.Contains(strings.Join(problems, "\n"), want) {
					t.Errorf("no problem %q in %q", want, problems)
				}
			}
			switch v.Echo {
			case "", "up":
			case "unreachable", "5xx", "timeout":
				if v.Connector != "block" && v.Connector != "send" {
					t.Errorf("a fail-closed vector needs connector block or send, not %q", v.Connector)
				}
				if (v.Mode == "seal") != (v.Connector == "block") {
					t.Errorf("mode %s with Echo %s must %s", v.Mode, v.Echo, map[bool]string{true: "block", false: "send"}[v.Mode == "seal"])
				}
				return
			default:
				t.Fatalf("echo %q", v.Echo)
			}
			if !v.Valid {
				return
			}
			s, err := New(Options{Mode: v.Mode})
			if err != nil {
				t.Fatal(err)
			}
			status, body := post(s, v.Capture, "")
			if status != http.StatusOK {
				t.Fatalf("HTTP %d: %s", status, body)
			}
			if p := sc.Decision([]byte(body)); len(p) > 0 {
				t.Errorf("Echo's decision breaks the schema: %q", p)
			}
			var got map[string]any
			_ = json.Unmarshal([]byte(body), &got)
			if !subset(v.Decision, got) {
				t.Errorf("decision = %s, want at least %v", body, v.Decision)
			}
		})
	}
}

// subset reports whether every field of want is in got with the same
// value, nested objects included.
func subset(want, got map[string]any) bool {
	for k, w := range want {
		if wm, ok := w.(map[string]any); ok {
			gm, ok := got[k].(map[string]any)
			if !ok || !subset(wm, gm) {
				return false
			}
			continue
		}
		if got[k] != w {
			return false
		}
	}
	return true
}

// request builds a request addressed to a loopback Echo, the way a
// connector reaches it. httptest's default Host, example.com, is one
// Echo's rebinding guard refuses.
func request(method, path string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, path, body)
	req.Host = "127.0.0.1:8025"
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

func post(s http.Handler, capture []byte, token string) (int, string) {
	req := request(http.MethodPost, "/v1/capture", bytes.NewReader(capture))
	if token != "" {
		req.Header.Set(wire.TokenHeader, token)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func vec(t *testing.T, file string) []byte {
	t.Helper()
	v, ok := vectors(t)[file]
	if !ok {
		t.Fatalf("no %s", file)
	}
	return v.Capture
}

func TestStandInPassesGoodCaptures(t *testing.T) {
	s, err := New(Options{Expect: []string{"email", "http-before", "run"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"capture-email-sealed.json", "capture-http-fake.json"} {
		if status, body := post(s, vec(t, f), ""); status != http.StatusOK {
			t.Fatalf("%s: HTTP %d %s", f, status, body)
		}
	}
	r := s.Report()
	if !r.OK || r.Captures != 2 {
		t.Fatalf("report = %+v", r)
	}
	var out bytes.Buffer
	r.Table(&out)
	for _, want := range []string{"schema", "PASS", "2 captures, all valid", "http-before", "1: an HTTP request reported before it is sent"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("table has no %q:\n%s", want, out.String())
		}
	}
	// The catcher behind it got them.
	if n := len(s.Catcher().Inbox.List(catcher.Filter{})); n != 2 {
		t.Errorf("inbox has %d messages", n)
	}
}

func TestStandInFailsABrokenCapture(t *testing.T) {
	s, _ := New(Options{})
	post(s, vec(t, "capture-email-sealed.json"), "")
	status, body := post(s, vec(t, "broken-unknown-field.json"), "")
	if status != http.StatusBadRequest || !strings.Contains(body, "conformance: capture.http.header: not part of the wire protocol") {
		t.Fatalf("broken capture: HTTP %d %s", status, body)
	}
	r := s.Report()
	if r.OK || len(r.Problems) != 1 {
		t.Fatalf("report = %+v", r)
	}
	var out bytes.Buffer
	r.Table(&out)
	if !strings.Contains(out.String(), "1 of 2 captures broke the wire schema") || !strings.Contains(out.String(), "refused capture 1:") || !strings.HasSuffix(out.String(), "FAIL\n") {
		t.Errorf("table:\n%s", out.String())
	}
	if n := len(s.Catcher().Inbox.List(catcher.Filter{})); n != 1 {
		t.Errorf("the broken capture reached the inbox: %d messages", n)
	}
}

func TestStandInFailsAMissingScenario(t *testing.T) {
	s, _ := New(Options{Expect: []string{"email", "http-after"}})
	post(s, vec(t, "capture-email-sealed.json"), "")
	r := s.Report()
	if r.OK {
		t.Fatal("passed without an http-after capture")
	}
	var out bytes.Buffer
	r.Table(&out)
	if !strings.Contains(out.String(), "http-after  FAIL    expected an allowed request's response reported after, saw none") {
		t.Errorf("table:\n%s", out.String())
	}
	if empty, _ := New(Options{}); empty.Report().OK {
		t.Error("no captures at all passed")
	}
	if _, err := New(Options{Expect: []string{"sms"}}); err == nil || !strings.Contains(err.Error(), "unknown scenario") {
		t.Errorf("unknown scenario: %v", err)
	}
}

// Speaking wire 1, the stand-in acts like an older Echo: health says 1, a
// v2 capture gets an older Echo's refusal. The report checks that the
// connector then sent v1.
func TestStandInWire1(t *testing.T) {
	s, err := New(Options{Wire: 1})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, request(http.MethodGet, "/v1/health", nil))
	var h map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &h); err != nil || h["wire"] != 1.0 || h["mode"] != "seal" {
		t.Fatalf("health = %s", rec.Body)
	}
	status, body := post(s, vec(t, "capture-email-sealed.json"), "")
	if status != http.StatusBadRequest || !strings.Contains(body, "newer than Echo") {
		t.Fatalf("v2 to a wire 1 Echo: HTTP %d %s", status, body)
	}
	if r := s.Report(); r.OK {
		t.Errorf("passed without falling back: %+v", r.Rows)
	}
	// v1 with a wire 2 field is still wrong.
	if status, _ := post(s, vec(t, "broken-v1-new-fields.json"), ""); status != http.StatusBadRequest {
		t.Errorf("v1 with wire 2 fields: HTTP %d", status)
	}
	if status, body := post(s, vec(t, "capture-v1.json"), ""); status != http.StatusOK {
		t.Fatalf("v1: HTTP %d %s", status, body)
	}
	r := s.Report()
	var fallback Row
	for _, row := range r.Rows {
		if row.Check == "fallback" {
			fallback = row
		}
	}
	if fallback.Result != "pass" || fallback.Detail != "refused 1 newer capture, then got wire 1" {
		t.Errorf("fallback = %+v", fallback)
	}
	if _, err := New(Options{Wire: 3}); err == nil {
		t.Error("accepted wire 3")
	}
}

func TestStandInToken(t *testing.T) {
	s, _ := New(Options{Token: "tok"})
	if status, _ := post(s, vec(t, "capture-email-sealed.json"), "nope"); status != http.StatusUnauthorized {
		t.Errorf("wrong token: HTTP %d", status)
	}
	if s.Report().Captures != 0 {
		t.Error("an unauthorized capture counted")
	}
	if status, _ := post(s, vec(t, "capture-email-sealed.json"), "tok"); status != http.StatusOK {
		t.Errorf("token: HTTP %d", status)
	}
	req := request(http.MethodGet, "/v1/conformance", nil)
	req.Header.Set(wire.TokenHeader, "tok")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	var r Report
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil || !r.OK || r.Captures != 1 {
		t.Errorf("GET /v1/conformance = %s", rec.Body)
	}
}

func TestServe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	addr := make(chan string, 1)
	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- serve(ctx, []string{"--listen", "127.0.0.1:0", "--expect", "email"}, &stdout, &stderr, func(a string) { addr <- a })
	}()
	var a string
	select {
	case a = <-addr:
	case <-time.After(5 * time.Second):
		t.Fatal("did not start")
	}
	resp, err := http.Post("http://"+a+"/v1/capture", "application/json", bytes.NewReader(vec(t, "capture-email-sealed.json")))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	cancel()
	if code := <-done; code != 0 {
		t.Fatalf("exit %d\n%s%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "stand-in Echo on http://127.0.0.1:") || !strings.Contains(stdout.String(), "1: an email reported before it is sent") || !strings.HasSuffix(stdout.String(), "\nPASS\n") {
		t.Errorf("stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}

	// Nothing arrived: exit 1, as JSON this time.
	ctx, cancel = context.WithCancel(context.Background())
	stdout.Reset()
	go func() {
		done <- serve(ctx, []string{"--listen", "127.0.0.1:0", "--json"}, &stdout, io.Discard, func(string) { cancel() })
	}()
	if code := <-done; code != 1 || !strings.Contains(stdout.String(), `"ok": false`) {
		t.Errorf("no captures: exit %d\n%s", code, stdout.String())
	}

	for _, args := range [][]string{{"--mode", "loud"}, {"--nope"}, {"extra"}, {"--wire", "9"}} {
		if code := serve(context.Background(), args, io.Discard, io.Discard, nil); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	stdout.Reset()
	if code := serve(context.Background(), []string{"--help"}, &stdout, io.Discard, nil); code != 0 || !strings.Contains(stdout.String(), "--expect <list>") {
		t.Errorf("--help: exit %d\n%s", code, stdout.String())
	}
}
