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

	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/wire"
)

func mailpitGet(t *testing.T, srv *httptest.Server, path string, v any) int {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusOK && v != nil {
		if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode
}

func mailpitDelete(t *testing.T, srv *httptest.Server, body string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/v1/messages", strings.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(b) != "ok" {
		t.Fatalf("delete: %s %q", resp.Status, b)
	}
}

func TestMailpitAPI(t *testing.T) {
	s := NewServer(NewInbox(100), Config{})
	srv := httptest.NewServer(s)
	defer srv.Close()
	raw := "From: Shop <shop@shop.test>\r\nTo: Ann Buyer <ann@example.com>\r\nCc: audit@shop.test\r\nSubject: Your order\r\n" +
		"Message-ID: <abc123@shop.test>\r\nContent-Type: text/html\r\n\r\n<style>p{color:red}</style><p>Thanks   for\n your order</p>"
	first, _ := s.Capture(wire.Capture{Version: 1, Kind: "email", Email: &wire.Email{Raw: raw}})
	second, _ := s.Capture(wire.Capture{Version: 1, Kind: "email", Email: &wire.Email{From: "wp@shop.test", To: []string{"bob@example.com"},
		Subject: "Welcome", Text: "Hello Bob"}})
	post(t, srv, twilioCapture(wire.Before)) // an SMS: not Mailpit's business

	var list struct {
		Total, Count int
		Messages     []struct {
			ID, MessageID, Subject, Snippet, Created string
			From                                     struct{ Name, Address string }
			To, Cc                                   []struct{ Name, Address string }
		}
	}
	if mailpitGet(t, srv, "/api/v1/messages", &list) != 200 || list.Total != 2 || list.Count != 2 || len(list.Messages) != 2 {
		t.Fatalf("list = %+v", list)
	}
	newest, older := list.Messages[0], list.Messages[1]
	if newest.ID != second.ID || older.ID != first.ID {
		t.Errorf("want newest first: %s, %s", newest.ID, older.ID)
	}
	if older.From.Name != "Shop" || older.From.Address != "shop@shop.test" || len(older.To) != 1 || older.To[0].Address != "ann@example.com" ||
		older.To[0].Name != "Ann Buyer" ||
		len(older.Cc) != 1 || older.Subject != "Your order" || older.MessageID != "abc123@shop.test" ||
		older.Snippet != "Thanks for your order" || older.Created == "" {
		t.Errorf("summary = %+v", older)
	}
	if mailpitGet(t, srv, "/api/v1/messages?start=1&limit=1", &list) != 200 || list.Total != 2 || list.Count != 1 || list.Messages[0].ID != first.ID {
		t.Errorf("paged = %+v", list)
	}

	var msg struct {
		ID, Subject, Text, HTML string
		Headers                 map[string][]string
	}
	if mailpitGet(t, srv, "/api/v1/message/"+first.ID, &msg) != 200 || msg.ID != first.ID || !strings.Contains(msg.HTML, "Thanks") ||
		msg.Headers["Message-Id"] == nil || msg.Headers["Subject"][0] != "Your order" {
		t.Errorf("message = %+v", msg)
	}
	if mailpitGet(t, srv, "/api/v1/message/latest", &msg) != 200 || msg.ID != second.ID || msg.Text != "Hello Bob" {
		t.Errorf("latest = %+v", msg)
	}
	var hdrs map[string][]string
	if mailpitGet(t, srv, "/api/v1/message/"+first.ID+"/headers", &hdrs) != 200 || hdrs["Cc"][0] != "audit@shop.test" {
		t.Errorf("headers = %v", hdrs)
	}
	sms := s.Inbox.List(Filter{Channel: model.SMS})[0]
	if code := mailpitGet(t, srv, "/api/v1/message/"+sms.ID, nil); code != http.StatusNotFound {
		t.Errorf("an SMS isn't a Mailpit message: %d", code)
	}

	mailpitDelete(t, srv, `{"IDs":["`+first.ID+`","`+sms.ID+`"]}`)
	if ms := s.Inbox.List(Filter{}); len(ms) != 2 || ms[0].ID != second.ID || ms[1].Channel != model.SMS {
		t.Errorf("after deleting one: %+v", ms)
	}
	mailpitDelete(t, srv, "")
	if ms := s.Inbox.List(Filter{}); len(ms) != 1 || ms[0].Channel != model.SMS {
		t.Errorf("deleting all emails keeps other messages: %+v", ms)
	}
	if mailpitGet(t, srv, "/api/v1/messages", &list) != 200 || list.Total != 0 || list.Messages == nil {
		t.Errorf("empty list = %+v", list)
	}
}

func TestMailpitAPINeedsTheToken(t *testing.T) {
	srv := httptest.NewServer(NewServer(NewInbox(1), Config{Token: "s3cret"}))
	defer srv.Close()
	if code := mailpitGet(t, srv, "/api/v1/messages", nil); code != http.StatusUnauthorized {
		t.Errorf("want 401 without token, got %d", code)
	}
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/messages", nil)
	req.Header.Set(wire.TokenHeader, "s3cret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("with token: %s", resp.Status)
	}
}
