// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package catcher

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestHealthGolden(t *testing.T) {
	s := NewServer(NewInbox(1), Config{Version: "1.2.3", SMTPAddr: "127.0.0.1:1025", ProxyAddr: "off", BootID: "boot-1", Store: "jsonl"})
	srv := httptest.NewServer(s)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	// The provider list grows with every recognizer: checked apart.
	var h map[string]json.RawMessage
	if err := json.Unmarshal(body, &h); err != nil {
		t.Fatal(err)
	}
	var providers []string
	if json.Unmarshal(h["providers"], &providers); len(providers) == 0 {
		t.Errorf("providers missing: %s", body)
	}
	got := bytes.Replace(body, h["providers"], []byte(`["…"]`), 1)
	want, err := os.ReadFile("testdata/health.golden")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("health =\n%s\nwant\n%s", got, want)
	}
}

func TestHealthDefaults(t *testing.T) {
	a, b := NewServer(NewInbox(1), Config{}), NewServer(NewInbox(1), Config{})
	if a.BootID() == "" || a.BootID() == b.BootID() {
		t.Errorf("each start gets its own boot ID: %q %q", a.BootID(), b.BootID())
	}
	srv := httptest.NewServer(a)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var h map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&h)
	if _, ok := h["smtp"]; ok {
		t.Errorf("an unknown SMTP address is left out: %v", h)
	}
	if h["store"] != "memory" || h["boot_id"] != a.BootID() {
		t.Errorf("defaults: %v", h)
	}
}
