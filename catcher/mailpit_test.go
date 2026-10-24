// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package catcher

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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

// fullEmail has every part Mailpit's shapes describe: names, Cc,
// Reply-To, List-Unsubscribe, an inline image and an attachment.
const fullEmail = "From: John Doe <john@example.com>\r\nTo: Jane Smith <jane@example.com>\r\nCc: audit@example.com\r\n" +
	"Reply-To: Support <support@example.com>\r\nReturn-Path: <bounces@example.com>\r\nSubject: Message subject\r\n" +
	"Message-ID: <12345.67890@localhost>\r\nList-Unsubscribe: <mailto:unsubscribe@example.com>, <https://example.com/unsubscribe>\r\n" +
	"List-Unsubscribe-Post: List-Unsubscribe=One-Click\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=\"mix\"\r\n\r\n" +
	"--mix\r\nContent-Type: multipart/related; boundary=\"rel\"\r\n\r\n" +
	"--rel\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p><img src=\"cid:logo\"> Your invoice</p>\r\n" +
	"--rel\r\nContent-Type: image/png\r\nContent-ID: <logo@example.com>\r\nContent-Disposition: inline; filename=\"logo.png\"\r\n" +
	"Content-Transfer-Encoding: base64\r\n\r\niVBORw0KGgo=\r\n--rel--\r\n" +
	"--mix\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=\"invoice.pdf\"\r\n" +
	"Content-Transfer-Encoding: base64\r\n\r\nJVBERi0xLjQK\r\n--mix--\r\n"

// shapeExtras are the keys Echo answers that Mailpit's shapes lack and
// shapeMissing the Mailpit keys Echo leaves out.
var (
	shapeExtras  = []string{"count", "Headers"}
	shapeMissing = []string{"PartID", "MD5", "SHA1"}
)

// sameShape reports where got differs from want in field names and JSON
// types. Arrays are compared by their first elements when both have one.
func sameShape(path string, want, got any) []string {
	kind := func(v any) string { return fmt.Sprintf("%T", v) }
	if want == nil || got == nil { // a null From is still an object
		return nil
	}
	if kind(want) != kind(got) {
		return []string{fmt.Sprintf("%s: Mailpit has %s, Echo %s", path, kind(want), kind(got))}
	}
	var diffs []string
	switch w := want.(type) {
	case map[string]any:
		g := got.(map[string]any)
		for k, v := range w {
			gv, ok := g[k]
			if !ok {
				if !slices.Contains(shapeMissing, k) {
					diffs = append(diffs, path+"."+k+": missing")
				}
				continue
			}
			diffs = append(diffs, sameShape(path+"."+k, v, gv)...)
		}
		for k := range g {
			if _, ok := w[k]; !ok && !slices.Contains(shapeExtras, k) {
				diffs = append(diffs, path+"."+k+": not in Mailpit's shape")
			}
		}
	case []any:
		if g := got.([]any); len(w) > 0 && len(g) > 0 {
			diffs = append(diffs, sameShape(path+"[0]", w[0], g[0])...)
		}
	}
	return diffs
}

func TestMailpitShapes(t *testing.T) {
	s := NewServer(NewInbox(10), Config{Version: "1.5.0"})
	srv := httptest.NewServer(s)
	defer srv.Close()
	d := post(t, srv, wire.Capture{Version: 1, Kind: "email", Source: wire.Source{Via: "smtp"}, Email: &wire.Email{Raw: fullEmail}})
	post(t, srv, wire.Capture{Version: 1, Kind: "email", Email: &wire.Email{From: "wp@shop.test", To: []string{"bob@example.com"}, Subject: "Other"}})
	for _, tt := range []struct{ golden, path string }{
		{"messages.json", "/api/v1/messages"},
		{"messages.json", "/api/v1/search?query=subject:%22message+subject%22"},
		{"message.json", "/api/v1/message/" + d.ID},
		{"info.json", "/api/v1/info"},
		{"webui.json", "/api/v1/webui"},
	} {
		b, err := os.ReadFile(filepath.Join("testdata", "mailpit", tt.golden))
		if err != nil {
			t.Fatal(err)
		}
		var want, got any
		if err := json.Unmarshal(b, &want); err != nil {
			t.Fatal(err)
		}
		if code := mailpitGet(t, srv, tt.path, &got); code != http.StatusOK {
			t.Fatalf("%s: %d", tt.path, code)
		}
		for _, d := range sameShape(tt.path, want, got) {
			t.Error(d)
		}
	}
	var msg mailpitMessage
	mailpitGet(t, srv, "/api/v1/message/"+d.ID, &msg)
	if len(msg.Inline) != 1 || msg.Inline[0].ContentID != "" || len(msg.Attachments) != 1 || msg.ReturnPath != "bounces@example.com" ||
		!slices.Equal(msg.ListUnsubscribe.Links, []string{"mailto:unsubscribe@example.com", "https://example.com/unsubscribe"}) ||
		msg.ListUnsubscribe.HeaderPost != "List-Unsubscribe=One-Click" {
		t.Errorf("message = %+v", msg)
	}
	var info mailpitInfo
	mailpitGet(t, srv, "/api/v1/info", &info)
	if info.Version != "1.5.0" || info.Database != "memory" || info.Messages != 2 || info.Unread != 1 ||
		info.RuntimeStats.SMTPAccepted != 1 || info.RuntimeStats.SMTPAcceptedSize != int64(len(fullEmail)) {
		t.Errorf("info = %+v", info)
	}
}

func TestMailpitSearchParity(t *testing.T) {
	s := NewServer(NewInbox(20), Config{})
	srv := httptest.NewServer(s)
	defer srv.Close()
	at := func(day int) time.Time { return time.Date(2026, 10, day, 12, 0, 0, 0, time.UTC) }
	add := func(m model.Message) string {
		m.Channel = model.Email
		return s.Inbox.Add(m).ID
	}
	order := add(model.Message{From: "Shop <shop@shop.test>", To: []string{"ann@example.com"}, CC: []string{"audit@shop.test"},
		Subject: "Your order shipped", Text: "Thanks for your order", Headers: map[string]string{"message-id": "<order-1@shop.test>"},
		Attachments: []model.Attachment{{Name: "invoice.pdf", Type: "application/pdf", Size: 3000}}, At: at(1),
		Source: model.Source{Site: "https://client-a.test"}})
	reset := add(model.Message{From: "wp@client-b.test", To: []string{"bob@example.com"}, BCC: []string{"log@client-b.test"},
		ReplyTo: "help@client-b.test", Subject: "Reset your password", Text: "Click the link to reset", At: at(5),
		Attachments: []model.Attachment{{Name: "logo.png", Type: "image/png", Size: 10, Inline: true}},
		Source:      model.Source{Site: "https://client-b.test"}})
	welcome := add(model.Message{From: "hello@shop.test", To: []string{"carol@example.com"}, Subject: "Welcome aboard",
		HTML: "<p>Welcome to the shop, " + strings.Repeat("x", 2048) + "</p>", At: at(9)})
	s.setRead([]model.Message{{ID: reset}}, true)

	for _, tt := range []struct {
		query string
		want  []string // newest first
	}{
		{"order", []string{order}},
		{`"order shipped"`, []string{order}},
		{"subject:welcome", []string{welcome}},
		{`subject:"reset your"`, []string{reset}},
		{"to:ann", []string{order}},
		{"to:audit", nil}, // Mailpit's to: is the To field alone
		{"cc:audit", []string{order}},
		{"bcc:log@", []string{reset}},
		{"reply-to:help", []string{reset}},
		{"addressed:audit", []string{order}},
		{"addressed:example.com", []string{welcome, reset, order}},
		{"from:shop.test", []string{welcome, order}},
		{"FROM:SHOP.TEST !welcome", []string{order}},
		{"from:shop.test -subject:welcome", []string{order}},
		{"message-id:order-1@shop.test", []string{order}},
		{"has:attachment", []string{order}},
		{"!has:attachment", []string{welcome, reset}},
		{"has:inline", []string{reset}},
		{"is:read", []string{reset}},
		{"is:unread", []string{welcome, order}},
		{"!is:read", []string{welcome, order}},
		{"tag:anything", nil},
		{"is:tagged", nil},
		{"!is:tagged", []string{welcome, reset, order}},
		{"larger:2K", []string{welcome}},
		{"smaller:1KB", []string{reset, order}},
		{"larger:huge", []string{welcome, reset, order}}, // a size that doesn't read is ignored
		{"after:2026/10/03", []string{welcome, reset}},
		{"before:2026/10/06", []string{reset, order}},
		{`after:"2026/10/05 11:00" before:2026-10-06`, []string{reset}},
		{"before:10/02/2026", []string{order}},
		{"after:someday", []string{welcome, reset, order}},
		{"site:client-a", []string{order}},
		{"https://client-b.test", nil}, // free text, not a field
		{"shop", []string{welcome, order}},
	} {
		var list mailpitList
		path := "/api/v1/search?tz=UTC&query=" + url.QueryEscape(tt.query)
		if code := mailpitGet(t, srv, path, &list); code != http.StatusOK {
			t.Errorf("%s: %d", tt.query, code)
			continue
		}
		var got []string
		for _, m := range list.Messages {
			got = append(got, m.ID)
		}
		if !slices.Equal(got, tt.want) || list.Total != 3 || list.MessagesCount != len(tt.want) {
			t.Errorf("%s: got %v (total %d, matched %d), want %v", tt.query, got, list.Total, list.MessagesCount, tt.want)
		}
	}
	for q, want := range map[string]string{
		"":               "query: Mailpit's search needs a query (use /api/v1/messages for every email)",
		`subject:"open`:  "query: a quote in the search isn't closed",
		"is:starred":     "query: is:starred: is takes read, unread or tagged",
		"has:pdf":        "query: has:pdf: has takes attachment, inline or issue",
		"subject:":       "query: subject: needs a value",
		"channel:sms":    "", // not a Mailpit field: free text, matching nothing
		"check:content/": "",
	} {
		resp, err := http.Get(srv.URL + "/api/v1/search?query=" + url.QueryEscape(q))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if want == "" {
			if resp.StatusCode != http.StatusOK {
				t.Errorf("%q: %s %s", q, resp.Status, b)
			}
			continue
		}
		if resp.StatusCode != http.StatusBadRequest || strings.TrimSpace(string(b)) != want {
			t.Errorf("%q: %s %q, want %q", q, resp.Status, b, want)
		}
	}
}

func mailpitDo(t *testing.T, srv *httptest.Server, method, path, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
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

func TestMailpitReadFlags(t *testing.T) {
	s := NewServer(NewInbox(10), Config{})
	srv := httptest.NewServer(s)
	defer srv.Close()
	a := s.Inbox.Add(model.Message{Channel: model.Email, Subject: "first"}).ID
	b := s.Inbox.Add(model.Message{Channel: model.Email, Subject: "second"}).ID
	unread := func() (n int, read map[string]bool) {
		var list mailpitList
		mailpitGet(t, srv, "/api/v1/messages", &list)
		read = map[string]bool{}
		for _, m := range list.Messages {
			read[m.ID] = m.Read
		}
		return list.Unread, read
	}
	if n, _ := unread(); n != 2 {
		t.Fatalf("unread = %d", n)
	}
	if code, body := mailpitDo(t, srv, http.MethodPut, "/api/v1/messages", `{"IDs":["`+a+`"],"Read":true}`); code != 200 || body != "ok" {
		t.Fatalf("put: %d %s", code, body)
	}
	if n, read := unread(); n != 1 || !read[a] || read[b] {
		t.Fatalf("after marking one: %d %v", n, read)
	}
	mailpitDo(t, srv, http.MethodPut, "/api/v1/messages", `{"Read":true}`)
	if n, _ := unread(); n != 0 {
		t.Fatalf("after marking all: %d", n)
	}
	mailpitDo(t, srv, http.MethodPut, "/api/v1/messages", `{"Search":"subject:second","Read":false}`)
	if n, read := unread(); n != 1 || read[b] {
		t.Fatalf("after a search: %d %v", n, read)
	}
	// Reading a message opens it, as in Mailpit.
	mailpitGet(t, srv, "/api/v1/message/"+b, nil)
	if n, _ := unread(); n != 0 {
		t.Fatalf("after reading: %d", n)
	}
	if code, body := mailpitDo(t, srv, http.MethodPut, "/api/v1/messages", `{"Search":"is:nope"}`); code != 400 ||
		!strings.Contains(body, "Search: is:nope") {
		t.Errorf("bad search: %d %s", code, body)
	}
}

func TestMailpitRawAndParts(t *testing.T) {
	s := NewServer(NewInbox(10), Config{})
	srv := httptest.NewServer(s)
	defer srv.Close()
	raw := post(t, srv, wire.Capture{Version: 1, Kind: "email", Source: wire.Source{Via: "smtp"}, Email: &wire.Email{Raw: fullEmail}})
	post(t, srv, wire.Capture{Version: 1, Kind: "email", Email: &wire.Email{From: "wp@shop.test", To: []string{"bob@example.com"},
		Subject: "Welcome", Text: "Hello Bob", HTML: "<p>Hello Bob</p>", Headers: map[string]string{"X-Mailer": "WordPress"}}})

	code, body := mailpitDo(t, srv, http.MethodGet, "/api/v1/message/"+raw.ID+"/raw", "")
	if code != 200 || !strings.Contains(body, "Subject: Message subject") || !strings.Contains(body, "invoice.pdf") {
		t.Errorf("raw source: %d %s", code, body)
	}
	code, body = mailpitDo(t, srv, http.MethodGet, "/api/v1/message/latest/raw", "")
	for _, want := range []string{"From: wp@shop.test\r\n", "To: bob@example.com\r\n", "Subject: Welcome\r\n", "X-Mailer: WordPress\r\n",
		`Content-Type: multipart/alternative; boundary="echo-alternative"`, "\r\n\r\n--echo-alternative\r\n", "Hello Bob\r\n", "<p>Hello Bob</p>"} {
		if code != 200 || !strings.Contains(body, want) {
			t.Errorf("rebuilt source lacks %q: %d %s", want, code, body)
		}
	}
	if p, err := ParseMIME([]byte(body)); err != nil || p.Text != "Hello Bob" || p.HTML != "<p>Hello Bob</p>" {
		t.Errorf("rebuilt source doesn't parse: %v %+v", err, p)
	}
	if code, body := mailpitDo(t, srv, http.MethodGet, "/api/v1/message/"+raw.ID+"/part/2", ""); code != 404 ||
		!strings.Contains(body, "attachment content is not stored") {
		t.Errorf("part: %d %s", code, body)
	}
	for _, path := range []string{"/api/v1/message/nope/raw", "/api/v1/message/nope/part/1"} {
		if code, body := mailpitDo(t, srv, http.MethodGet, path, ""); code != 404 || !strings.Contains(body, "message not found") {
			t.Errorf("%s: %d %s", path, code, body)
		}
	}
}
