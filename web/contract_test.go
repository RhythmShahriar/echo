// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package web

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/check"
)

// TestAPIContract drives the paths app.js fetches against a real
// catcher.Server holding 10,000 messages, served the way up serves them.
func TestAPIContract(t *testing.T) {
	js, _ := static.ReadFile("static/app.js")
	for name, want := range map[string]string{"API": PathMessages, "EVENTS": PathEvents} {
		m := regexp.MustCompile(`const ` + name + ` = "([^"]+)"`).FindSubmatch(js)
		if m == nil || string(m[1]) != want {
			t.Fatalf("app.js %s = %q, the server serves %q", name, m, want)
		}
	}

	inbox := catcher.NewInbox(10000)
	srv := catcher.NewServer(inbox, catcher.Config{Mode: catcher.Seal})
	ui := New(Options{Inbox: inbox, Mode: catcher.Seal, Checks: check.Options{SiteHost: "shop.test"}})
	inbox.OnAdd(ui.Notify)
	for i := range 10000 {
		inbox.Add(sample(i))
	}
	mux := http.NewServeMux()
	mux.Handle("/v1/", srv)
	mux.Handle("/", ui)
	base := testServer(t, mux)

	fetch := func(method, path string) (int, []byte) {
		t.Helper()
		req, _ := http.NewRequest(method, base+path, nil) //nolint:noctx // test
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}

	start := time.Now()
	code, body := fetch("GET", PathMessages+"?limit=50&q=")
	took := time.Since(start)
	var p Page
	if err := json.Unmarshal(body, &p); err != nil || code != 200 {
		t.Fatalf("first page: %d %v", code, err)
	}
	if len(body) > 100<<10 {
		t.Errorf("first page is %d bytes, want under 100 KB", len(body))
	}
	if len(p.Messages) != 50 || p.Inbox != 10000 || p.Next == "" {
		t.Errorf("first page: %d rows of %d, next %q", len(p.Messages), p.Inbox, p.Next)
	}
	t.Logf("first page of 10,000: %d bytes in %v", len(body), took)
	start = time.Now()
	fetch("GET", PathMessages+"?limit=50&q=")
	t.Logf("again (checks cached): %v", time.Since(start))

	code, body = fetch("GET", PathMessages+"?limit=50&before="+p.Next+"&q=channel%3Aemail")
	var older Page
	if err := json.Unmarshal(body, &older); err != nil || code != 200 || len(older.Messages) != 50 || older.Messages[0].At.After(p.Messages[49].At) {
		t.Errorf("older page: %d %v", code, err)
	}

	id := p.Messages[0].ID
	code, body = fetch("GET", PathMessages+"/"+id)
	var d Detail
	if err := json.Unmarshal(body, &d); err != nil || code != 200 || d.Message.ID != id {
		t.Errorf("detail: %d %v", code, err)
	}
	email := older.Messages[0].ID
	if code, body := fetch("GET", PathMessages+"/"+email+"/html"); code != 200 || !strings.Contains(string(body), "Thanks for your order") {
		t.Errorf("html frame: %d", code)
	}

	resp, err := http.Get(base + PathEvents) //nolint:noctx // test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	events := readEvents(t, resp.Body)
	waitSubscribed(t, ui)
	// A capture through the catcher's own API reaches the page as an event.
	capture := `{"v":1,"kind":"email","source":{"via":"wordpress"},"email":{"from":"shop@shop.test","to":["bob@example.org"],"subject":"Welcome"}}`
	cr, err := http.Post(base+"/v1/capture", "application/json", strings.NewReader(capture)) //nolint:noctx // test
	if err != nil {
		t.Fatal(err)
	}
	var dec struct{ ID string }
	_ = json.NewDecoder(cr.Body).Decode(&dec)
	_ = cr.Body.Close()
	if e := next(t, events); e.Type != "add" || e.ID != dec.ID {
		t.Errorf("event %+v, want add %s", e, dec.ID)
	}
	if code, _ := fetch("DELETE", PathMessages+"/"+dec.ID); code != http.StatusNoContent {
		t.Errorf("delete: %d", code)
	}
	if e := next(t, events); e.Type != "delete" || e.ID != dec.ID {
		t.Errorf("event %+v, want delete", e)
	}
	if code, _ := fetch("GET", "/v1/messages/"+dec.ID); code != http.StatusNotFound {
		t.Errorf("the catcher still has the deleted message: %d", code)
	}
	ui.Close()
}
