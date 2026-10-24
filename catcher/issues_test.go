// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package catcher

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/check"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/wire"
)

func getMessage(t *testing.T, srv *httptest.Server, id string) model.Message {
	t.Helper()
	resp, err := http.Get(srv.URL + "/v1/messages/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var m model.Message
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	return m
}

func checks(issues []model.Issue) []string {
	var out []string
	for _, i := range issues {
		out = append(out, i.Check)
	}
	return out
}

func TestEveryCaptureIsChecked(t *testing.T) {
	s := NewServer(NewInbox(10), Config{Site: "https://shop.example"})
	srv := httptest.NewServer(s)
	defer srv.Close()
	bad := post(t, srv, wire.Capture{Version: 1, Kind: "email", Source: wire.Source{Via: "test"}, Email: &wire.Email{
		From: "shop@shop.example", To: []string{"buyer@example.com"}, Subject: "Welcome",
		Text: "Hi {first_name}, confirm at http://localhost/confirm", HTML: "<p>Hi {first_name}</p>"}})
	m := getMessage(t, srv, bad.ID)
	got := checks(m.Issues)
	for _, want := range []string{"content/placeholder", "content/dev-link"} {
		if !slices.Contains(got, want) {
			t.Errorf("issues = %q, want %s", got, want)
		}
	}
	if m.Issues[0].Message != m.ID {
		t.Errorf("an issue names its message: %+v", m.Issues[0])
	}

	clean := post(t, srv, wire.Capture{Version: 1, Kind: "email", Source: wire.Source{Via: "test"}, Email: &wire.Email{
		From: "shop@shop.example", To: []string{"buyer@example.com"}, Subject: "Welcome",
		Text: "Hi Ada, confirm at https://shop.example/confirm", HTML: "<p>Hi Ada, <a href=\"https://shop.example/confirm\">confirm</a></p>"}})
	if m := getMessage(t, srv, clean.ID); len(m.Issues) != 0 {
		t.Errorf("a clean email has no issues: %+v", m.Issues)
	}

	resp, err := http.Get(srv.URL + "/v1/messages")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var list []model.Message
	_ = json.NewDecoder(resp.Body).Decode(&list)
	if len(list) != 2 || len(list[0].Issues) == 0 || len(list[1].Issues) != 0 {
		t.Errorf("the list carries issues too: %+v", list)
	}
}

// TestCapturesFollowTheProjectsChecks: the config's rules: and checks:
// reach what every capture carries, as they do a run's report.
func TestCapturesFollowTheProjectsChecks(t *testing.T) {
	rules, err := check.CustomRules([]check.Custom{{ID: "brand/secret-project", Match: "SECRET-PROJECT", Level: model.Error}})
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(NewInbox(10), Config{Site: "https://shop.example", Rules: rules,
		Checks: map[string]string{"content/placeholder": check.Off, "content/dev-link": "notice"}})
	srv := httptest.NewServer(s)
	defer srv.Close()
	d := post(t, srv, wire.Capture{Version: 1, Kind: "email", Source: wire.Source{Via: "test"}, Email: &wire.Email{
		From: "shop@shop.example", To: []string{"buyer@example.com"}, Subject: "SECRET-PROJECT launch",
		Text: "Hi {first_name}, see http://localhost/launch"}})
	m := getMessage(t, srv, d.ID)
	levels := map[string]model.Level{}
	for _, i := range m.Issues {
		levels[i.Check] = i.Level
	}
	if levels["brand/secret-project"] != model.Error {
		t.Errorf("the project's rule: %v", m.Issues)
	}
	if _, on := levels["content/placeholder"]; on {
		t.Errorf("checks: off still reports content/placeholder: %v", m.Issues)
	}
	if levels["content/dev-link"] != model.Notice {
		t.Errorf("checks: notice: %v", m.Issues)
	}
}

func TestUpdateRechecks(t *testing.T) {
	s := NewServer(NewInbox(10), Config{Mode: Observe})
	c := wire.Capture{Version: 1, Kind: "http", Phase: wire.Before, Source: wire.Source{Via: "test"},
		HTTP: &wire.HTTP{Method: "POST", URL: "https://hooks.example.net/order", Body: `{"id":42}`}}
	d, err := s.Capture(c)
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := s.Inbox.Get(d.ID); slices.Contains(checks(m.Issues), "send/failed") {
		t.Fatalf("not sent yet: %+v", m.Issues)
	}
	c.Phase, c.ID, c.HTTP.Status = wire.After, d.ID, 500
	if _, err := s.Capture(c); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.Inbox.Get(d.ID); !slices.Contains(checks(m.Issues), "send/failed") {
		t.Errorf("after a 500 the message fails: %+v", m.Issues)
	}
}

func TestInboxWithoutCheckKeepsIssues(t *testing.T) {
	in := NewInbox(10)
	given := []model.Issue{{Check: "x/y"}}
	if m := in.Add(model.Message{Issues: given}); len(m.Issues) != 1 {
		t.Errorf("no check set, issues kept: %+v", m.Issues)
	}
}

// TestSignInCodesAreMasked: a code named by "to sign in" or "to log in"
// is masked in what the inbox keeps, as one named "code" is.
func TestSignInCodesAreMasked(t *testing.T) {
	s := NewServer(NewInbox(10), Config{})
	srv := httptest.NewServer(s)
	defer srv.Close()
	for _, text := range []string{"Use 482913 to sign in.", "Enter 551203 to log in."} {
		d := post(t, srv, wire.Capture{Version: 1, Kind: "email", Source: wire.Source{Via: "test"}, Email: &wire.Email{
			From: "shop@shop.example", To: []string{"ann@example.com"}, Subject: "Welcome", Text: text}})
		m := getMessage(t, srv, d.ID)
		if strings.ContainsAny(m.Text, "0123456789") {
			t.Errorf("%q is stored as %q", text, m.Text)
		}
	}
}
