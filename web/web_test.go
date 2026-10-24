// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package web

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/check"
	"github.com/RhythmShahriar/echo/model"
)

func newUI(n int) (*UI, *catcher.Inbox) {
	inbox := catcher.NewInbox(10000)
	for i := range n {
		inbox.Add(sample(i))
	}
	u := New(Options{Inbox: inbox, Mode: catcher.Seal, Checks: check.Options{SiteHost: "shop.test"}})
	return u, inbox
}

// sample is message i of a varied inbox: emails with big bodies, SMS and
// webhooks.
func sample(i int) model.Message {
	at := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Second)
	switch i % 3 {
	case 0:
		return model.Message{ID: fmt.Sprintf("e%05d", i), At: at, Channel: model.Email, From: "shop@shop.test", To: []string{fmt.Sprintf("ann%d@example.org", i)},
			Subject: fmt.Sprintf("Order #%d confirmed", i), Text: strings.Repeat("Thanks for your order. ", 40),
			HTML: "<p>" + strings.Repeat("Thanks for your order. ", 40) + "</p>", Status: model.Faked, Action: "checkout"}
	case 1:
		return model.Message{ID: fmt.Sprintf("s%05d", i), At: at, Channel: model.SMS, Provider: "twilio", To: []string{"+14155550101"},
			Text: fmt.Sprintf("Your code is %d", i), Status: model.Faked, Method: "POST", URL: "https://api.twilio.com/2010-04-01/Accounts/AC0/Messages.json"}
	}
	return model.Message{ID: fmt.Sprintf("w%05d", i), At: at, Channel: model.Webhook, Provider: "zapier", Event: "order.created",
		To: []string{"hooks.zapier.com/hooks/catch/1/ab•••••"}, Body: strings.Repeat(`{"id":1042}`, 300), Status: model.Faked, Method: "POST",
		URL: "https://hooks.zapier.com/hooks/catch/1/ab•••••/", Headers: map[string]string{"content-type": "application/json"}}
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Host = "127.0.0.1:8025"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// TestPageIsSmallWhateverTheInbox: / is the same static page with 0 or
// 10,000 messages, well under 200 KB, under the strict CSP.
func TestPageIsSmallWhateverTheInbox(t *testing.T) {
	empty, _ := newUI(0)
	full, _ := newUI(10000)
	a, b := get(empty, "/"), get(full, "/")
	if a.Code != 200 || b.Code != 200 {
		t.Fatalf("status %d %d", a.Code, b.Code)
	}
	if a.Body.String() != b.Body.String() || b.Body.Len() > 200<<10 {
		t.Errorf("page is %d bytes with 10,000 messages, %d without", b.Body.Len(), a.Body.Len())
	}
	if got := b.Header().Get("Content-Security-Policy"); got != "default-src 'self'; img-src 'self' data:; style-src 'self'; frame-src 'self'; script-src 'self'" {
		t.Errorf("CSP = %q", got)
	}
	if b.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(b.Header().Get("Content-Type"), "text/html") {
		t.Errorf("headers: %v", b.Header())
	}
}

// TestAssetsAreEmbedded: everything the page loads is served from the
// binary, small, from Echo itself and fits the CSP (no inline script or
// style for the browser to refuse).
func TestAssetsAreEmbedded(t *testing.T) {
	u, _ := newUI(0)
	total := 0
	_ = fs.WalkDir(static, "static", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := fs.ReadFile(static, path)
			total += len(b)
		}
		return err
	})
	if total == 0 || total > 150<<10 {
		t.Errorf("embedded assets are %d bytes, want 1 to 150 KB", total)
	}
	page := get(u, "/").Body.String()
	for _, ref := range regexp.MustCompile(`(?:src|href)="([^"]+)"`).FindAllStringSubmatch(page, -1) {
		switch {
		case ref[1] == "data:,":
		case strings.HasPrefix(ref[1], "/assets/"):
			w := get(u, ref[1])
			if w.Code != 200 || w.Body.Len() == 0 || w.Header().Get("Content-Security-Policy") == "" {
				t.Errorf("%s: %d, %d bytes", ref[1], w.Code, w.Body.Len())
			}
		default:
			t.Errorf("page loads %s, which isn't an embedded asset", ref[1])
		}
	}
	if regexp.MustCompile(`<script>|<style|\sstyle=|\son[a-z]+=`).MatchString(page) {
		t.Error("index.html has inline script or style, which the CSP refuses")
	}
	if js := get(u, "/assets/app.js").Body.String(); strings.Contains(js, "innerHTML") || strings.Contains(js, "http://") || strings.Contains(js, "https://") {
		t.Error("app.js writes HTML or reaches outside Echo")
	}
	if w := get(u, "/assets/"); w.Code != http.StatusNotFound {
		t.Errorf("asset listing: %d", w.Code)
	}
}

func TestListPagesNewestFirst(t *testing.T) {
	u, _ := newUI(120)
	var p Page
	if err := json.Unmarshal(get(u, PathMessages).Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Messages) != 50 || p.Total != 120 || p.Inbox != 120 || p.Messages[0].ID != sample(119).ID || p.Next != p.Messages[49].ID {
		t.Fatalf("first page: %d rows, total %d, first %s, next %s", len(p.Messages), p.Total, p.Messages[0].ID, p.Next)
	}
	seen := map[string]bool{}
	pages := 1
	for p.Next != "" {
		for _, s := range p.Messages {
			if seen[s.ID] {
				t.Fatalf("%s on two pages", s.ID)
			}
			seen[s.ID] = true
		}
		next := p.Next
		p = Page{}
		_ = json.Unmarshal(get(u, PathMessages+"?before="+next).Body.Bytes(), &p)
		pages++
	}
	for _, s := range p.Messages {
		seen[s.ID] = true
	}
	if len(seen) != 120 || pages != 3 {
		t.Errorf("%d messages over %d pages", len(seen), pages)
	}
	_ = json.Unmarshal(get(u, PathMessages+"?limit=5&q=channel:sms").Body.Bytes(), &p)
	if len(p.Messages) != 5 || p.Total != 40 || p.Messages[0].Channel != model.SMS {
		t.Errorf("sms search: %d rows of %d", len(p.Messages), p.Total)
	}
	if strings.Join(p.Facets.Channels, ",") != "email,sms,webhook" || strings.Join(p.Facets.Providers, ",") != "twilio,zapier" || strings.Join(p.Facets.Actions, ",") != "checkout" {
		t.Errorf("facets: %+v", p.Facets)
	}
}

func TestDetailHasChecksAndAttachments(t *testing.T) {
	u, inbox := newUI(0)
	inbox.Add(model.Message{ID: "m1", Channel: model.Email, Subject: "Hi {first_name}", Text: "Hi", Status: model.Faked, Body: invoiceMIME})
	var d Detail
	if err := json.Unmarshal(get(u, PathMessages+"/m1").Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Issues) == 0 || d.Issues[0].Check != "content/placeholder" {
		t.Errorf("issues: %+v", d.Issues)
	}
	if len(d.Attachments) != 1 || d.Attachments[0].Name != "invoice-42.pdf" || d.Raw != invoiceMIME {
		t.Errorf("attachments %+v, raw %q", d.Attachments, d.Raw)
	}
	if w := get(u, PathMessages+"/nope"); w.Code != http.StatusNotFound {
		t.Errorf("unknown ID: %d", w.Code)
	}
}

// TestMessageHTMLIsSandboxed: the HTML tab's frame runs no script, loads
// no remote image until asked and links open in a new tab.
func TestMessageHTMLIsSandboxed(t *testing.T) {
	u, inbox := newUI(0)
	inbox.Add(model.Message{ID: "m1", Channel: model.Email, HTML: `<p style="color:red">Hi</p><img src="https://track.example/p.gif"><script>alert(1)</script>`})
	inbox.Add(model.Message{ID: "m2", Channel: model.SMS, Text: "no html"})
	w := get(u, PathMessages+"/m1/html")
	csp := w.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "style-src 'unsafe-inline'", "img-src data:;", "sandbox allow-popups"} {
		if !strings.Contains(csp, want) {
			t.Errorf("frame CSP %q lacks %q", csp, want)
		}
	}
	if strings.Contains(csp, "script-src") || strings.Contains(csp, "allow-scripts") || strings.Contains(csp, "allow-same-origin") {
		t.Errorf("frame CSP allows scripts or the page's origin: %q", csp)
	}
	if !strings.HasPrefix(w.Body.String(), `<!doctype html><meta charset="utf-8"><base target="_blank">`) {
		t.Errorf("frame body: %q", w.Body.String())
	}
	if csp := get(u, PathMessages+"/m1/html?images=1").Header().Get("Content-Security-Policy"); !strings.Contains(csp, "img-src * data:") {
		t.Errorf("remote images on: %q", csp)
	}
	if w := get(u, PathMessages+"/m2/html"); w.Code != http.StatusNotFound {
		t.Errorf("SMS has no HTML frame: %d", w.Code)
	}
	if !strings.Contains(get(u, "/assets/app.js").Body.String(), `"/html" + (state.images.has(id) ? "?images=1" : "")`) {
		t.Error("the page no longer loads the frame the way this test checks")
	}
}

func TestDeleteAndHostGuards(t *testing.T) {
	u, inbox := newUI(3)
	del := func(id, host, origin string) int {
		r := httptest.NewRequest(http.MethodDelete, PathMessages+"/"+id, nil)
		r.Host = host
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		u.ServeHTTP(w, r)
		return w.Code
	}
	if code := del("e00000", "127.0.0.1:8025", "https://evil.example"); code != http.StatusForbidden {
		t.Errorf("cross-site delete: %d", code)
	}
	if code := del("e00000", "127.0.0.1:8025", "http://127.0.0.1:8025"); code != http.StatusNoContent {
		t.Errorf("delete: %d", code)
	}
	if _, ok := inbox.Get("e00000"); ok {
		t.Error("still in the inbox")
	}
	if code := del("e00000", "127.0.0.1:8025", ""); code != http.StatusNotFound {
		t.Errorf("delete again: %d", code)
	}
	for _, host := range []string{"localhost:8025", "[::1]:8025", "echo.localhost"} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Host = host
		w := httptest.NewRecorder()
		u.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Errorf("Host %s: %d", host, w.Code)
		}
	}
	r := httptest.NewRequest(http.MethodGet, PathMessages, nil)
	r.Host = "rebind.evil.example"
	w := httptest.NewRecorder()
	u.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("rebinding Host: %d", w.Code)
	}
	open := New(Options{Inbox: inbox, AnyHost: true})
	w = httptest.NewRecorder()
	open.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("AnyHost: %d", w.Code)
	}
}

// testServer serves h on a loopback port in 20400-20419.
func testServer(t *testing.T, h http.Handler) string {
	t.Helper()
	for port := 20400; port < 20420; port++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		srv := &http.Server{Handler: h, ReadHeaderTimeout: time.Second}
		go func() { _ = srv.Serve(ln) }()
		t.Cleanup(func() { _ = srv.Close() })
		return "http://" + ln.Addr().String()
	}
	t.Skip("no free port in 20400-20419")
	return ""
}

// readEvents returns the events of a stream, one per data line.
func readEvents(t *testing.T, body io.Reader) <-chan Event {
	t.Helper()
	ch := make(chan Event, 16)
	go func() {
		defer close(ch)
		sc := bufio.NewScanner(body)
		for sc.Scan() {
			if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				var e Event
				if json.Unmarshal([]byte(data), &e) == nil {
					ch <- e
				}
			}
		}
	}()
	return ch
}

func next(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(5 * time.Second):
		t.Fatal("no event")
		return Event{}
	}
}

func TestEventsStreamAndClose(t *testing.T) {
	u, inbox := newUI(0)
	inbox.OnAdd(u.Notify)
	base := testServer(t, u)
	resp, err := http.Get(base + PathEvents) //nolint:noctx // test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	events := readEvents(t, resp.Body)
	waitSubscribed(t, u)
	m := inbox.Add(model.Message{ID: "x1", Channel: model.SMS, Status: model.Faked})
	if e := next(t, events); e != (Event{"add", "x1"}) {
		t.Errorf("got %+v", e)
	}
	inbox.Update(m.ID, func(m *model.Message) { m.Status = model.Failed })
	if e := next(t, events); e != (Event{"update", "x1"}) {
		t.Errorf("got %+v", e)
	}
	req, _ := http.NewRequest(http.MethodDelete, base+PathMessages+"/x1", nil) //nolint:noctx // test
	if r, err := http.DefaultClient.Do(req); err == nil {
		_ = r.Body.Close()
	}
	if e := next(t, events); e != (Event{"delete", "x1"}) {
		t.Errorf("got %+v", e)
	}
	u.Close()
	select {
	case _, open := <-events:
		if open {
			t.Error("an event after Close")
		}
	case <-time.After(5 * time.Second):
		t.Error("the stream stayed open after Close")
	}
}

func waitSubscribed(t *testing.T, u *UI) {
	t.Helper()
	for i := 0; ; i++ {
		u.events.mu.Lock()
		n := len(u.events.subs)
		u.events.mu.Unlock()
		if n > 0 {
			return
		}
		if i > 250 {
			t.Fatal("never subscribed")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestSlowStreamDoesNotBlockTheInbox: a subscriber that never reads loses
// events instead of holding up Add.
func TestSlowStreamDoesNotBlockTheInbox(t *testing.T) {
	h := newHub()
	ch, _ := h.subscribe()
	done := make(chan struct{})
	go func() {
		for i := range 1000 {
			h.publish("", fmt.Sprint(i))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("publish blocked on a slow subscriber")
	}
	if len(ch) != cap(ch) {
		t.Errorf("buffer holds %d of %d", len(ch), cap(ch))
	}
	h.close()
	if _, ok := h.subscribe(); ok {
		t.Error("subscribed after close")
	}
}
