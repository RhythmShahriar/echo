// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package catcher

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/codes"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/wire"
)

func getLatest(t *testing.T, srv *httptest.Server, query string) (int, latestAnswer, string) {
	t.Helper()
	resp, err := http.Get(srv.URL + "/v1/messages/latest?" + query)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	var a latestAnswer
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(b, &a); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode, a, string(b)
}

// stored is everything the inbox shows of the messages: the list and
// each message.
func stored(t *testing.T, srv *httptest.Server, ids ...string) string {
	t.Helper()
	_, all := getJSON(t, srv, "/v1/messages", nil)
	for _, id := range ids {
		_, one := getJSON(t, srv, "/v1/messages/"+id, nil)
		all += one
	}
	return all
}

func TestLatestCodesAndLinks(t *testing.T) {
	s := NewServer(NewInbox(10), Config{})
	srv := httptest.NewServer(s)
	defer srv.Close()
	reset := "https://app.test/reset-password/Zq81mXr2Lp0aT7wY4kVn?email=ann%40example.com"
	mail := post(t, srv, wire.Capture{Version: 1, Kind: "email", Run: "r1/login", Source: wire.Source{Via: "laravel"},
		Email: &wire.Email{To: []string{"ann@example.com"}, Subject: "Sign in to Acme",
			HTML: `<p>Enter <b>482913</b> to sign in. Your verification code expires soon.</p>` +
				`<p><a href="` + reset + `">Forgot your password?</a></p>`}})
	sms := post(t, srv, twilioLogin("615204"))

	code, a, body := getLatest(t, srv, "to=ann@&channel=email")
	if code != http.StatusOK || strings.Join(a.Codes, ",") != "482913" || a.Message.ID != mail.ID {
		t.Fatalf("latest email: %d %s", code, body)
	}
	if len(a.Links) != 1 || a.Links[0] != (codes.Link{Kind: codes.Reset, URL: reset}) {
		t.Errorf("links %+v", a.Links)
	}
	if code, a, body = getLatest(t, srv, "channel=email,sms"); code != http.StatusOK || a.Message.ID != sms.ID || strings.Join(a.Codes, ",") != "615204" {
		t.Errorf("latest of email and sms: %d %s", code, body)
	}
	if _, a, _ = getLatest(t, srv, "action=login"); a.Message.ID != mail.ID {
		t.Errorf("by action: %s", a.Message.ID)
	}
	// What's stored, listed and returned as the message never has them.
	msg, _ := json.Marshal(a.Message)
	all := stored(t, srv, mail.ID, sms.ID) + string(msg)
	for _, secret := range []string{"482913", "615204", "Zq81mXr2Lp0aT7wY4kVn"} {
		if strings.Contains(all, secret) {
			t.Errorf("%s is stored: %s", secret, all)
		}
	}
	if !strings.Contains(all, "https://app.test/reset-password/"+"•••••") {
		t.Errorf("the reset link isn't kept masked: %s", all)
	}
}

// twilioLogin is a sealed Twilio SMS carrying a login code.
func twilioLogin(code string) wire.Capture {
	c := twilioCapture(wire.Before)
	c.Run = ""
	c.HTTP.Body = "To=%2B15551230000&From=%2B15550001111&Body=" + code + "+is+your+Acme+verification+code"
	return c
}

func TestLatestWaitsForTheMessage(t *testing.T) {
	s := NewServer(NewInbox(10), Config{})
	srv := httptest.NewServer(s)
	defer srv.Close()
	s.Inbox.Add(model.Message{Channel: model.Email, To: []string{"old@example.com"}})
	since := time.Now().UTC().Format(time.RFC3339Nano)
	got := make(chan latestAnswer, 1)
	go func() {
		_, a, _ := getLatest(t, srv, "channel=sms&wait=10s&since="+since)
		got <- a
	}()
	// Nothing matches yet: the request is still waiting.
	select {
	case a := <-got:
		t.Fatalf("answered before the message came: %+v", a)
	case <-time.After(100 * time.Millisecond):
	}
	post(t, srv, twilioLogin("770312"))
	select {
	case a := <-got:
		if strings.Join(a.Codes, ",") != "770312" {
			t.Errorf("codes %v", a.Codes)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("latest didn't answer when the message came")
	}
}

func TestLatestErrors(t *testing.T) {
	s := NewServer(NewInbox(10), Config{})
	srv := httptest.NewServer(s)
	defer srv.Close()
	if code, _, body := getLatest(t, srv, "channel=sms&wait=20ms"); code != http.StatusNotFound || !strings.Contains(body, "no message matched within 20ms") {
		t.Errorf("none: %d %q", code, body)
	}
	if code, _, body := getLatest(t, srv, ""); code != http.StatusNotFound || strings.TrimSpace(body) != "no message matched" {
		t.Errorf("none, no wait: %d %q", code, body)
	}
	if code, _, body := getLatest(t, srv, "channel=email,fax"); code != http.StatusBadRequest || !strings.Contains(body, "fax is not a channel") {
		t.Errorf("bad channel: %d %q", code, body)
	}
	// A message without codes is still the latest: it says none came.
	s.Inbox.Add(model.Message{Channel: model.Email, Subject: "Hello"})
	if code, a, _ := getLatest(t, srv, ""); code != http.StatusOK || a.Codes == nil || len(a.Codes) != 0 || a.Links == nil {
		t.Errorf("no codes: %d %+v", code, a)
	}

	locked := httptest.NewServer(NewServer(NewInbox(10), Config{Token: "t0k"}))
	defer locked.Close()
	if code, _, _ := getLatest(t, locked, ""); code != http.StatusUnauthorized {
		t.Errorf("without the token: %d", code)
	}
}

func TestFoundCodesLeaveWithTheMessage(t *testing.T) {
	in := NewInbox(1)
	a := in.add(model.Message{Channel: model.SMS}, found{codes: []string{"123456"}})
	in.add(model.Message{Channel: model.SMS}, found{codes: []string{"654321"}}) // drops a
	in.mu.Lock()
	_, kept := in.found[a.ID]
	n := len(in.found)
	in.mu.Unlock()
	if kept || n != 1 {
		t.Errorf("found after eviction: %v, %d entries", kept, n)
	}
	in.Clear(Filter{})
	if len(in.found) != 0 {
		t.Errorf("found after clear: %d", len(in.found))
	}
}

func TestHidePath(t *testing.T) {
	for in, want := range map[string]string{
		"https://app.test/reset-password/Zq81mXr2Lp0aT7wY4kVn?email=a": "https://app.test/reset-password/•••••?email=a",
		"https://app.test/email/verify/7/9f8e?signature=•••••":         "https://app.test/email/verify/7/9f8e?signature=•••••",
		"https://app.test/very-long-plain-words-path/x":                "https://app.test/very-long-plain-words-path/x",
		"https://Zq81mXr2Lp0aT7wY4kVn.app.test/":                       "https://Zq81mXr2Lp0aT7wY4kVn.app.test/",
	} {
		if got := hidePath(in); got != want {
			t.Errorf("hidePath(%q) = %q, want %q", in, got, want)
		}
	}
}
