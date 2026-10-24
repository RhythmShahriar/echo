// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package image

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/wire"
)

// pending are Mailpit fields Echo doesn't serve yet. The catcher's Mailpit
// parity work (C14) adds them; a field listed here may be missing. When
// it is there its type must match.
var pending = []string{"messages_unread", "messages[].Username", "Username", "ListUnsubscribe"}

// jsonType names v's JSON type as encoding/json decodes it into any.
func jsonType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return fmt.Sprintf("%T", v)
}

// shapeDiff compares got with want by field names and JSON types and
// returns what differs, one line per field. Fields Echo adds are fine; a
// field Mailpit has is missing or has another type is not. Arrays compare
// each element Echo sent with Mailpit's first element. Paths name array
// elements as [] ("messages[].To[].Address").
func shapeDiff(want, got any, path string) []string {
	if wt, gt := jsonType(want), jsonType(got); wt != gt {
		return []string{fmt.Sprintf("%s: Mailpit has %s, Echo %s", path, wt, gt)}
	}
	var out []string
	switch w := want.(type) {
	case map[string]any:
		g := got.(map[string]any)
		keys := make([]string, 0, len(w))
		for k := range w {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			p := k
			if path != "" {
				p = path + "." + k
			}
			gv, ok := g[k]
			if !ok {
				if !slices.Contains(pending, p) {
					out = append(out, p+": missing")
				}
				continue
			}
			out = append(out, shapeDiff(w[k], gv, p)...)
		}
	case []any:
		g := got.([]any)
		if len(w) == 0 {
			return nil
		}
		for _, gv := range g {
			out = append(out, shapeDiff(w[0], gv, path+"[]")...)
		}
	}
	return out
}

func recorded(t *testing.T, name string) any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "mailpit", name))
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// getJSON fetches base+path with Echo's token (when set) and decodes it.
func getJSON(t *testing.T, base, token, path string) any {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set(wire.TokenHeader, token)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %s", path, resp.Status)
	}
	var v any
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return v
}

// waitMessages polls the Mailpit list until it holds a message whose
// subject is subject and returns the list.
func waitMessages(t *testing.T, base, token, subject string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		list, _ := getJSON(t, base, token, "/api/v1/messages").(map[string]any)
		msgs, _ := list["messages"].([]any)
		for _, m := range msgs {
			if mm, _ := m.(map[string]any); mm["Subject"] == subject {
				return list
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no message %q in /api/v1/messages: %v", subject, list)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// checkParity compares Echo's list and message answers at base with the
// recorded Mailpit shapes. The inbox must hold a message with subject.
func checkParity(t *testing.T, base, token, subject string) {
	t.Helper()
	list := waitMessages(t, base, token, subject)
	for _, d := range shapeDiff(recorded(t, "messages.json"), list, "") {
		t.Errorf("GET /api/v1/messages: %s", d)
	}
	first := list["messages"].([]any)[0].(map[string]any)
	id, _ := first["ID"].(string)
	msg := getJSON(t, base, token, "/api/v1/message/"+id)
	for _, d := range shapeDiff(recorded(t, "message.json"), msg, "") {
		t.Errorf("GET /api/v1/message/{ID}: %s", d)
	}
}

// testMail is the message both tests send: display names, Cc, Reply-To
// and a Message-ID, so every address field has a value to compare.
func testMail(subject string) string {
	return "From: Shop <shop@shop.test>\r\nTo: Ann Buyer <ann@example.com>\r\nCc: audit@shop.test\r\n" +
		"Reply-To: help@shop.test\r\nSubject: " + subject + "\r\nMessage-ID: <abc123@shop.test>\r\n" +
		"Return-Path: <bounce@shop.test>\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nThanks for your order.\r\n"
}

func TestMailpitShapeInProcess(t *testing.T) {
	s := catcher.NewServer(catcher.NewInbox(100), catcher.Config{})
	srv := httptest.NewServer(s)
	defer srv.Close()
	if _, err := s.Capture(wire.Capture{Version: wire.Version, Kind: "email", Source: wire.Source{Via: "smtp"},
		Email: &wire.Email{Raw: testMail("Your order")}}); err != nil {
		t.Fatal(err)
	}
	checkParity(t, srv.URL, "", "Your order")
}

func TestShapeDiff(t *testing.T) {
	want := map[string]any{"a": "x", "b": []any{map[string]any{"c": 1.0}}, "messages_unread": 1.0}
	for _, tc := range []struct {
		name string
		got  any
		diff []string
	}{
		{"same", map[string]any{"a": "y", "b": []any{map[string]any{"c": 2.0}}, "messages_unread": 0.0}, nil},
		{"extra fields and a pending one missing", map[string]any{"a": "y", "b": []any{}, "z": true}, nil},
		{"missing", map[string]any{"b": []any{}}, []string{"a: missing"}},
		{"wrong type", map[string]any{"a": nil, "b": []any{map[string]any{"c": "1"}}},
			[]string{"a: Mailpit has string, Echo null", "b[].c: Mailpit has number, Echo string"}},
		{"pending with the wrong type", map[string]any{"a": "", "b": []any{}, "messages_unread": "1"},
			[]string{"messages_unread: Mailpit has number, Echo string"}},
		{"not an object", []any{}, []string{": Mailpit has object, Echo array"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := shapeDiff(want, tc.got, "")
			if strings.Join(got, "\n") != strings.Join(tc.diff, "\n") {
				t.Errorf("got %q, want %q", got, tc.diff)
			}
		})
	}
}
