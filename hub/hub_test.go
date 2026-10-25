// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package hub

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/web"
	"github.com/RhythmShahriar/echo/wire"
)

// Fixture ports on loopback. TestTLS reuses the hub's port, since no
// other test listens while it runs.
const (
	hubAddr   = "127.0.0.1:20504"
	smtpAddr  = "127.0.0.1:20505"
	shopAAddr = "127.0.0.1:20506"
	shopBAddr = "127.0.0.1:20507"
	tlsAddr   = hubAddr
)

var tokens = map[string]string{"READ": "read-token-0001", "TOKEN_A": "write-token-aaaa", "TOKEN_B": "write-token-bbbb"}

// writeSites writes a sites.yaml for client-a and client-b in dir.
func writeSites(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "sites.yaml")
	yaml := `read_token_env: READ
sites:
  - id: client-a
    token_env: TOKEN_A
    config: a/.echo/config.yaml
    schedule: "Tue 02:00"
  - id: client-b
    token_env: TOKEN_B
    config: b/.echo/config.yaml
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func getenv(k string) string { return tokens[k] }

func newHub(t *testing.T, store string) *Hub {
	t.Helper()
	sites, err := LoadSites(writeSites(t, t.TempDir()), getenv)
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(Options{Store: store, Sites: sites, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func listen(t *testing.T, addr string, h http.Handler) *httptest.Server {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("listen %s: %v", addr, err)
	}
	srv := httptest.NewUnstartedServer(h)
	_ = srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

func do(t *testing.T, method, url, token, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set(wire.TokenHeader, token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func emailCapture(subject, to string) string {
	b, _ := json.Marshal(wire.Capture{Version: wire.Version, Kind: "email", Source: wire.Source{Via: "wordpress"},
		Email: &wire.Email{From: "shop@shop.test", To: []string{to}, Subject: subject, Text: "hello"}})
	return string(b)
}

func subjects(t *testing.T, body string) []string {
	t.Helper()
	var ms []model.Message
	if err := json.Unmarshal([]byte(body), &ms); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	var out []string
	for _, m := range ms {
		out = append(out, m.Subject)
	}
	return out
}

func TestTokensAndSites(t *testing.T) {
	h := newHub(t, t.TempDir())
	defer func() { _ = h.Close() }()
	base := listen(t, hubAddr, h).URL
	read, a, b := tokens["READ"], tokens["TOKEN_A"], tokens["TOKEN_B"]

	code, body := do(t, http.MethodPost, base+"/v1/capture", a, emailCapture("From A", "ann@example.com"))
	if code != http.StatusOK {
		t.Fatalf("site A capture: %d %s", code, body)
	}
	var decision wire.Decision
	_ = json.Unmarshal([]byte(body), &decision)
	if code, body := do(t, http.MethodPost, base+"/v1/capture", b, emailCapture("From B", "bob@example.com")); code != http.StatusOK {
		t.Fatalf("site B capture: %d %s", code, body)
	}

	for _, tc := range []struct {
		name, method, path, token, body string
		want                            int
	}{
		{"site token can't list", http.MethodGet, "/v1/messages", a, "", http.StatusForbidden},
		{"site token can't read one", http.MethodGet, "/v1/messages/" + decision.ID, a, "", http.StatusForbidden},
		{"site token can't use the Mailpit API", http.MethodGet, "/api/v1/messages", a, "", http.StatusForbidden},
		{"site token can't delete", http.MethodDelete, "/v1/messages", a, "", http.StatusForbidden},
		{"site token can't list sites", http.MethodGet, "/v1/sites", a, "", http.StatusForbidden},
		{"site token has health", http.MethodGet, "/v1/health", a, "", http.StatusOK},
		{"no token", http.MethodGet, "/v1/messages", "", "", http.StatusUnauthorized},
		{"wrong token", http.MethodGet, "/v1/messages", "write-token-cccc", "", http.StatusUnauthorized},
		{"read token can't capture", http.MethodPost, "/v1/capture", read, emailCapture("x", "x@example.com"), http.StatusForbidden},
		{"another site's message can't be updated", http.MethodPost, "/v1/capture", b,
			`{"version":1,"kind":"email","phase":"after","id":"` + decision.ID + `","error":"bounced"}`, http.StatusForbidden},
		{"read token reads one", http.MethodGet, "/v1/messages/" + decision.ID, read, "", http.StatusOK},
		{"unknown site", http.MethodGet, "/v1/messages?site=client-z", read, "", http.StatusNotFound},
		{"since isn't a time", http.MethodGet, "/v1/messages?since=30d", read, "", http.StatusBadRequest},
		{"since is a time", http.MethodGet, "/v1/messages?since=2026-10-24T12:00:00Z", read, "", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if code, body := do(t, tc.method, base+tc.path, tc.token, tc.body); code != tc.want {
				t.Errorf("%d, want %d: %s", code, tc.want, body)
			}
		})
	}

	_, body = do(t, http.MethodGet, base+"/v1/messages", read, "")
	if got := strings.Join(subjects(t, body), ","); got != "From A,From B" {
		t.Errorf("read token sees %q", got)
	}
	_, body = do(t, http.MethodGet, base+"/v1/messages?site=client-a", read, "")
	if got := strings.Join(subjects(t, body), ","); got != "From A" {
		t.Errorf("site filter gives %q", got)
	}
	_, body = do(t, http.MethodGet, base+"/v1/messages?site=client-b&channel=email", read, "")
	if got := strings.Join(subjects(t, body), ","); got != "From B" {
		t.Errorf("site and channel filter gives %q", got)
	}
	_, body = do(t, http.MethodGet, base+"/v1/messages?since=2999-01-01T00:00:00Z", read, "")
	if got := subjects(t, body); len(got) != 0 {
		t.Errorf("since in the future gives %q", got)
	}
	_, body = do(t, http.MethodGet, base+"/v1/sites", read, "")
	var rows []SiteInfo
	if err := json.Unmarshal([]byte(body), &rows); err != nil || len(rows) != 2 || rows[0].ID != "client-a" || rows[0].Messages != 1 ||
		rows[0].Schedule != "Tue 02:00" || rows[1].Messages != 1 {
		t.Errorf("sites: %s", body)
	}
	if got := h.SiteOf(decision.ID); got != "client-a" {
		t.Errorf("SiteOf = %q", got)
	}
}

// TestActionMarkersPerSite checks that an action marker tags only its own
// site's untagged mail, from a write token or from the read token with
// the site named.
func TestActionMarkersPerSite(t *testing.T) {
	h := newHub(t, t.TempDir())
	defer func() { _ = h.Close() }()
	read, a, b := tokens["READ"], tokens["TOKEN_A"], tokens["TOKEN_B"]
	serve := func(method, path, token, site, body string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set(wire.TokenHeader, token)
		if site != "" {
			req.Header.Set(SiteHeader, site)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	captured := func(token, subject string) model.Message {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/v1/capture", strings.NewReader(emailCapture(subject, "ann@example.com")))
		req.Header.Set(wire.TokenHeader, token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var d wire.Decision
		if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil || rec.Code != http.StatusOK {
			t.Fatalf("capture %q: %d %s", subject, rec.Code, rec.Body)
		}
		m, _ := h.Inbox.Get(d.ID)
		return m
	}
	begin := func(run string) string { return `{"run":"` + run + `","action":"contact"}` }

	if code := serve(http.MethodPost, "/v1/actions/begin", a, "", begin("run-a")); code != http.StatusNoContent {
		t.Fatalf("site A begin: %d", code)
	}
	if m := captured(b, "B during A's action"); m.Run != "" {
		t.Errorf("site B's mail went to site A's run %q", m.Run)
	}
	if m := captured(a, "A during its action"); m.Run != "run-a" || m.Action != "contact" {
		t.Errorf("site A's mail: run %q action %q", m.Run, m.Action)
	}
	if code := serve(http.MethodPost, "/v1/actions/end", a, "", ""); code != http.StatusNoContent {
		t.Fatalf("site A end: %d", code)
	}
	if m := captured(a, "A after"); m.Run != "" {
		t.Errorf("after end: run %q", m.Run)
	}

	// The read token names the site, as RunWindow does.
	if code := serve(http.MethodPost, "/v1/actions/begin", read, "client-b", begin("window-b")); code != http.StatusNoContent {
		t.Fatalf("read begin: %d", code)
	}
	if m := captured(a, "A during B's window"); m.Run != "" {
		t.Errorf("site A's mail went to site B's window %q", m.Run)
	}
	if m := captured(b, "B during its window"); m.Run != "window-b" {
		t.Errorf("site B's mail: run %q", m.Run)
	}
	_ = serve(http.MethodPost, "/v1/actions/end", read, "client-b", "")
	if code := serve(http.MethodPost, "/v1/actions/begin", read, "client-z", begin("x")); code != http.StatusNotFound {
		t.Errorf("unknown site: %d", code)
	}

	// Without a site, the read token's marker is for every site, as on a
	// plain catcher.
	_ = serve(http.MethodPost, "/v1/actions/begin", read, "", begin("all"))
	if m := captured(a, "A, all"); m.Run != "all" {
		t.Errorf("site A without a site marker: run %q", m.Run)
	}
	_ = serve(http.MethodPost, "/v1/actions/end", read, "", "")
	if m := captured(b, "B, none"); m.Run != "" {
		t.Errorf("after the read end: run %q", m.Run)
	}
}

func TestSMTPSites(t *testing.T) {
	h := newHub(t, t.TempDir())
	defer func() { _ = h.Close() }()
	ln, err := net.Listen("tcp", smtpAddr)
	if err != nil {
		t.Fatal(err)
	}
	sink := h.SMTP(smtpAddr, nil)
	go func() { _ = sink.Serve(ln) }()
	defer func() { _ = sink.Close() }()
	msg := func(subject string) []byte {
		return []byte("From: shop@shop.test\r\nTo: ann@example.com\r\nSubject: " + subject + "\r\n\r\nhello\r\n")
	}
	if err := smtp.SendMail(smtpAddr, smtp.PlainAuth("", "any", tokens["TOKEN_B"], "127.0.0.1"), "shop@shop.test", []string{"ann@example.com"}, msg("By SMTP")); err != nil {
		t.Fatal(err)
	}
	if err := smtp.SendMail(smtpAddr, nil, "shop@shop.test", []string{"ann@example.com"}, msg("No login")); err == nil || !strings.Contains(err.Error(), "530") {
		t.Errorf("without a login: %v", err)
	}
	if err := smtp.SendMail(smtpAddr, smtp.PlainAuth("", "any", tokens["READ"], "127.0.0.1"), "shop@shop.test", []string{"ann@example.com"}, msg("Read token")); err == nil {
		t.Error("the read token logged in to send")
	}
	ms := h.Inbox.List(catcher.Filter{})
	if len(ms) != 1 || ms[0].Subject != "By SMTP" || h.SiteOf(ms[0].ID) != "client-b" {
		t.Errorf("inbox: %+v", ms)
	}
}

func TestPersist(t *testing.T) {
	store := t.TempDir()
	h := newHub(t, store)
	for i, s := range []string{"First", "Second", "Third"} {
		tok := tokens["TOKEN_A"]
		if i == 1 {
			tok = tokens["TOKEN_B"]
		}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/capture", strings.NewReader(emailCapture(s, "ann@example.com")))
		req.Header.Set(wire.TokenHeader, tok)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("capture: %d %s", rec.Code, rec.Body)
		}
	}
	// Delete the second through the API: it must stay deleted.
	second := h.Inbox.List(catcher.Filter{})[1]
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/messages", strings.NewReader(`{"IDs":["`+second.ID+`"]}`))
	req.Header.Set(wire.TokenHeader, tokens["READ"])
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(httptest.NewRecorder(), req)
	// Health names the store the hub really keeps.
	if code, body := serveOne(t, h, http.MethodGet, "/v1/health", tokens["READ"]); code != http.StatusOK || !strings.Contains(body, `"store":"jsonl"`) {
		t.Errorf("health: %d %s", code, body)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(store, InboxDir)); err != nil || runtime.GOOS != "windows" && fi.Mode().Perm() != 0o700 {
		t.Errorf("inbox folder: %v %v", fi, err)
	}

	h2 := newHub(t, store)
	defer func() { _ = h2.Close() }()
	ms := h2.Inbox.List(catcher.Filter{})
	var got []string
	for _, m := range ms {
		got = append(got, m.Subject+"@"+h2.SiteOf(m.ID))
	}
	if strings.Join(got, ",") != "First@client-a,Third@client-a" {
		t.Errorf("after a restart: %q", got)
	}
	if ms[0].ID == "" || ms[0].At.IsZero() {
		t.Errorf("IDs or times lost: %+v", ms[0])
	}
}

// serveOne makes one request to h directly and returns its answer.
func serveOne(t *testing.T, h http.Handler, method, path, token string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set(wire.TokenHeader, token)
	}
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// TestMigrateInboxFile: the inbox.jsonl an earlier hub kept moves into
// the catcher's inbox once, each message marked with its site; a torn
// last line is dropped and the file goes.
func TestMigrateInboxFile(t *testing.T) {
	store := t.TempDir()
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	var lines []string
	for _, r := range []record{
		{Site: "client-a", Message: model.Message{ID: "m1", Channel: model.Email, Subject: "Old A", To: []string{"a@example.com"}, At: at}},
		{Site: "client-ab", Message: model.Message{ID: "m2", Channel: model.Email, Subject: "Old AB", To: []string{"b@example.com"}, At: at}},
		{Site: "client-a", Message: model.Message{ID: "m1", Channel: model.Email, Subject: "Old A", To: []string{"a@example.com"}, At: at,
			Status: model.Sent}},
	} {
		b, _ := json.Marshal(r)
		lines = append(lines, string(b))
	}
	data := strings.Join(lines, "\n") + "\n" + `{"site":"client-a","message":{"id":"torn","subj`
	if err := os.WriteFile(filepath.Join(store, legacyInbox), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newHub(t, store)
	ms := h.Inbox.List(catcher.Filter{})
	if len(ms) != 2 || ms[0].ID != "m1" || ms[0].Source.Site != "client-a" || ms[0].Status != model.Sent || ms[1].Source.Site != "client-ab" ||
		!ms[0].At.Equal(at) {
		t.Errorf("migrated: %+v", ms)
	}
	if _, err := os.Stat(filepath.Join(store, legacyInbox)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the old file is still there: %v", err)
	}
	_ = h.Close()
	h2 := newHub(t, store)
	defer func() { _ = h2.Close() }()
	if n := len(h2.Inbox.List(catcher.Filter{})); n != 2 {
		t.Errorf("after a restart: %d messages", n)
	}
}

// TestSiteSearch: the hub marks what each site sends, so site: in a
// search finds one site's mail and never a site whose ID only starts the
// same; Echo Inbox opens with the read token and searches the same way.
func TestSiteSearch(t *testing.T) {
	dir := t.TempDir()
	yaml := "read_token_env: READ\nsites:\n  - {id: client-a, token_env: TOKEN_A}\n  - {id: client-ab, token_env: TOKEN_B}\n"
	if err := os.WriteFile(filepath.Join(dir, "sites.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	sites, err := LoadSites(filepath.Join(dir, "sites.yaml"), getenv)
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(Options{Store: t.TempDir(), Sites: sites})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = h.Close() }()
	srv := httptest.NewServer(h)
	defer srv.Close()
	do(t, http.MethodPost, srv.URL+"/v1/capture", tokens["TOKEN_A"], emailCapture("From A", "ann@example.com"))
	do(t, http.MethodPost, srv.URL+"/v1/capture", tokens["TOKEN_B"], emailCapture("From AB", "bob@example.com"))
	for _, path := range []string{"/v1/messages?q=site:client-a", "/v1/messages?site=client-a"} {
		_, body := do(t, http.MethodGet, srv.URL+path, tokens["READ"], "")
		if got := strings.Join(subjects(t, body), ","); got != "From A" {
			t.Errorf("%s: %q", path, got)
		}
	}
	_, body := do(t, http.MethodGet, srv.URL+"/v1/messages?q=site:client-a&limit=10", tokens["READ"], "")
	if !strings.Contains(body, `"messages":[`) || strings.Contains(body, "From AB") {
		t.Errorf("a page: %s", body)
	}
	// Echo Inbox: 401 without the read token, then ?token= once and the
	// cookie carries it.
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, tc := range []struct {
		path string
		want int
	}{{"/", http.StatusUnauthorized}, {"/?token=" + tokens["TOKEN_A"], http.StatusUnauthorized}, {"/?token=" + tokens["READ"], http.StatusSeeOther}} {
		resp, err := noRedirect.Get(srv.URL + tc.path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("GET %s: %d, want %d", tc.path, resp.StatusCode, tc.want)
		}
		if tc.want != http.StatusSeeOther {
			continue
		}
		if loc := resp.Header.Get("Location"); loc != "/" || len(resp.Cookies()) != 1 || !resp.Cookies()[0].HttpOnly {
			t.Fatalf("redirect %q, cookies %v", loc, resp.Cookies())
		}
		cookie := resp.Cookies()[0]
		get := func(path string) (int, string) {
			req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
			req.AddCookie(cookie)
			got, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = got.Body.Close() }()
			b, _ := io.ReadAll(got.Body)
			return got.StatusCode, string(b)
		}
		if code, b := get(web.PathMessages + "?q=site:client-a"); code != http.StatusOK || !strings.Contains(b, "From A") || strings.Contains(b, "From AB") {
			t.Errorf("Inbox search: %d %s", code, b)
		}
		if code, b := get("/"); code != http.StatusOK || !strings.Contains(b, "<html") {
			t.Errorf("Inbox page: %d %.80s", code, b)
		}
		// The cookie opens only Echo Inbox, not the API.
		if code, _ := get("/v1/messages"); code != http.StatusUnauthorized {
			t.Errorf("the cookie opened the API: %d", code)
		}
	}
}

func TestKeep(t *testing.T) {
	store := t.TempDir()
	sites, _ := LoadSites(writeSites(t, t.TempDir()), getenv)
	h, err := New(Options{Store: store, Sites: sites, Keep: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"1", "2", "3", "4", "5", "6"} {
		_ = h.as("client-a", func() error {
			_, err := h.srv.Capture(wire.Capture{Version: wire.Version, Kind: "email", Email: &wire.Email{Subject: s, To: []string{"a@example.com"}}})
			return err
		})
	}
	_ = h.Close()
	h2, err := New(Options{Store: store, Sites: sites, Keep: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = h2.Close() }()
	if ms := h2.Inbox.List(catcher.Filter{}); len(ms) != 2 || ms[1].Subject != "6" {
		t.Errorf("kept %+v", ms)
	}
}

func TestLoadSites(t *testing.T) {
	for _, tc := range []struct {
		name, yaml, want string
		env              map[string]string
	}{
		{"no read token", "sites: [{id: a, token_env: TOKEN_A}]", "set RHYTHM_ECHO_TOKEN", map[string]string{"TOKEN_A": "x"}},
		{"no sites", "read_token_env: READ\n", "no sites", tokens},
		{"bad id", "read_token_env: READ\nsites: [{id: 'a b', token_env: TOKEN_A}]", `id "a b"`, tokens},
		{"twice", "read_token_env: READ\nsites: [{id: a, token_env: TOKEN_A}, {id: a, token_env: TOKEN_B}]", "listed twice", tokens},
		{"unset token", "read_token_env: READ\nsites: [{id: a, token_env: NOPE}]", "set NOPE", tokens},
		{"shared token", "read_token_env: READ\nsites: [{id: a, token_env: TOKEN_A}, {id: b, token_env: TOKEN_A}]", "share a token", tokens},
		{"read token reused", "read_token_env: READ\nsites: [{id: a, token_env: READ}]", "is the read token", tokens},
		{"unknown key", "read_token_env: READ\nsites: [{id: a, token_env: TOKEN_A, tokn: x}]", "tokn", tokens},
		{"good", "read_token_env: READ\nsites: [{id: a, token_env: TOKEN_A, config: a/.echo/config.yaml}]", "", tokens},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "sites.yaml")
			_ = os.WriteFile(path, []byte(tc.yaml), 0o600)
			s, err := LoadSites(path, func(k string) string { return tc.env[k] })
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				if got := s.Sites[0].Config; got != filepath.Join(dir, "a", ".echo", "config.yaml") {
					t.Errorf("config path %q isn't relative to the file", got)
				}
				if got := s.Sites[0].ProjectDir(); got != filepath.Join(dir, "a") {
					t.Errorf("project dir %q", got)
				}
				if s.ReadToken() != tc.env["READ"] || s.WriteTokens()[tc.env["TOKEN_A"]] != "a" || len(s.WriteTokens()) != 1 {
					t.Errorf("tokens: read %q, write %v", s.ReadToken(), s.WriteTokens())
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %v, want %q", err, tc.want)
			}
		})
	}
}

func TestTLS(t *testing.T) {
	store := t.TempDir()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	cfg, err := SelfSigned(store, []string{"hub.agency.test"}, now)
	if err != nil {
		t.Fatal(err)
	}
	fp1, _ := Fingerprint(cfg)
	again, err := SelfSigned(store, []string{"hub.agency.test"}, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if fp2, _ := Fingerprint(again); fp1 != fp2 {
		t.Error("a second start made a new certificate; connectors that trust the old one would break")
	}
	if renewed, _ := SelfSigned(store, []string{"other.agency.test"}, now); renewed != nil {
		if fp3, _ := Fingerprint(renewed); fp3 == fp1 {
			t.Error("a new host name kept a certificate that doesn't cover it")
		}
	}
	cert, key := TLSFiles(store)
	if fi, err := os.Stat(key); err != nil || runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("key file: %v %v", fi, err)
	}
	cfg, err = LoadTLS(cert, key)
	if err != nil {
		t.Fatal(err)
	}

	h := newHub(t, t.TempDir())
	defer func() { _ = h.Close() }()
	ln, err := net.Listen("tcp", tlsAddr)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h, TLSConfig: cfg, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.ServeTLS(ln, "", "") }()
	defer func() { _ = srv.Close() }()

	hc, err := hubClient("https://"+tlsAddr, "", store)
	if err != nil || hc == nil {
		t.Fatalf("hubClient: %v", err)
	}
	c := client.New("https://"+tlsAddr, tokens["READ"])
	c.HTTP = hc
	if h, err := c.Health(context.Background()); err != nil || h.Tool != "rhythm-echo" {
		t.Fatalf("health over TLS: %+v %v", h, err)
	}
	// A client that doesn't trust the certificate can't talk to the hub.
	plain := client.New("https://"+tlsAddr, tokens["READ"])
	if _, err := plain.Health(context.Background()); err == nil {
		t.Error("an untrusting client connected")
	}
	var unknown x509.UnknownAuthorityError
	resp, err := (&http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}}).Get("https://" + tlsAddr + "/v1/health")
	if err == nil {
		_ = resp.Body.Close()
	}
	if !errors.As(err, &unknown) {
		t.Errorf("want an unknown authority error, got %v", err)
	}
}

func TestMainUsage(t *testing.T) {
	dir := t.TempDir()
	sitesFile := writeSites(t, dir)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--sites", sitesFile}, "--store and --sites are required"},
		{[]string{"--store", dir, "--sites", sitesFile, "--seal", "--observe"}, "pick --seal or --observe"},
		{[]string{"--store", dir, "--sites", sitesFile, "--tls-cert", "c.pem"}, "go together"},
		{[]string{"--store", dir, "--sites", sitesFile, "--tls-self-signed", "--tls-cert", "c", "--tls-key", "k"}, "not both"},
		{[]string{"--store", dir, "--sites", sitesFile, "--keep", "0"}, "--keep"},
		{[]string{"--store", dir, "--sites", sitesFile, "extra"}, `unexpected argument "extra"`},
		{[]string{"--store", dir, "--sites", sitesFile}, "set READ"}, // the tokens aren't in this process's environment
	} {
		var errb bytes.Buffer
		if code := Main(tc.args, nil, io.Discard, &errb); code != 2 || !strings.Contains(errb.String(), tc.want) {
			t.Errorf("%q: %d %q, want 2 and %q", tc.args, code, errb.String(), tc.want)
		}
	}
	var errb bytes.Buffer
	if code := Main([]string{"--help"}, nil, io.Discard, &errb); code != 0 || !strings.Contains(errb.String(), "Usage:\n  rhythm echo hub --store DIR") ||
		!strings.Contains(errb.String(), "\nExit codes: 0 stopped") {
		t.Errorf("--help: %d %q", code, errb.String())
	}
	errb.Reset()
	if code := Main([]string{"--bogus"}, nil, io.Discard, &errb); code != 2 || errb.String() != "rhythm echo hub: unknown flag --bogus\n" {
		t.Errorf("an unknown flag: %d %q", code, errb.String())
	}
	errb.Reset()
	if code := Main([]string{"--keep", "x"}, nil, io.Discard, &errb); code != 2 || !strings.Contains(errb.String(), `--keep "x": want a whole number`) {
		t.Errorf("a bad number: %d %q", code, errb.String())
	}
}

// TestShortTokens: a hub that listens beyond loopback refuses tokens
// shorter than MinToken and says which variables hold them.
func TestShortTokens(t *testing.T) {
	dir := t.TempDir()
	path := writeSites(t, dir)
	env := map[string]string{"READ": "read-token-0001", "TOKEN_A": "short", "TOKEN_B": "write-token-bbbb"}
	sites, err := LoadSites(path, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if got := sites.ShortTokens(); !slices.Equal(got, []string{"READ", "TOKEN_A"}) {
		t.Errorf("ShortTokens = %q", got)
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	for _, args := range [][]string{
		{"--listen", "0.0.0.0:22305", "--smtp", "off"},
		{"--listen", "127.0.0.1:22305", "--smtp", "0.0.0.0:22306"},
	} {
		var errb bytes.Buffer
		code := Main(append([]string{"--store", dir, "--sites", path}, args...), nil, io.Discard, &errb)
		if code != 2 || !strings.Contains(errb.String(), "READ, TOKEN_A: shorter than 16 characters") || !strings.Contains(errb.String(), "openssl rand -hex 16") {
			t.Errorf("%q: %d %s", args, code, errb.String())
		}
	}
}

// TestServe starts the hub the way Main does and stops it.
func TestServe(t *testing.T) {
	h := newHub(t, t.TempDir())
	defer func() { _ = h.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	var errb bytes.Buffer
	done := make(chan int)
	go func() { done <- serve(ctx, h, hubAddr, smtpAddr, nil, "store", &errb) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if code, _ := doQuiet(http.MethodGet, "http://"+hubAddr+"/v1/health", tokens["READ"]); code == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the hub didn't start")
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	if code := <-done; code != 0 {
		t.Errorf("exit %d: %s", code, errb.String())
	}
	for _, want := range []string{"Echo hub (seal mode): 2 sites", "API   http://" + hubAddr, "SMTP  " + smtpAddr, "Site  client-a  (write token: TOKEN_A)"} {
		if !strings.Contains(errb.String(), want) {
			t.Errorf("banner has no %q:\n%s", want, errb.String())
		}
	}
}

func doQuiet(method, url, token string) (int, error) {
	req, _ := http.NewRequest(method, url, nil)
	req.Header.Set(wire.TokenHeader, token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

// TestHubAnswersUnderItsOwnName: the hub listens beyond loopback, so the
// catcher behind it must not refuse a Host it doesn't know once the hub
// let the request in. A token is still needed.
func TestHubAnswersUnderItsOwnName(t *testing.T) {
	h := newHub(t, t.TempDir())
	defer func() { _ = h.Close() }()
	for _, tc := range []struct {
		method, path, token string
		want                int
	}{
		{http.MethodGet, "/v1/health", tokens["TOKEN_A"], http.StatusOK},
		{http.MethodGet, "/api/v1/messages", tokens["READ"], http.StatusOK},
		{http.MethodDelete, "/v1/messages", tokens["READ"], http.StatusOK},
		{http.MethodGet, "/api/v1/messages", "", http.StatusUnauthorized},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Host = "hub.agency.example:8025"
		if tc.token != "" {
			req.Header.Set(wire.TokenHeader, tc.token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s %s: %d %s, want %d", tc.method, tc.path, rec.Code, rec.Body, tc.want)
		}
	}
}
