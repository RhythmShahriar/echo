// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package signal

import (
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
)

func TestMetaCalls(t *testing.T) {
	for _, cmd := range []string{"revoke", "grant"} {
		s := hook(t, record(t, "fbq", "consent", cmd))
		if s == nil || s.Source != "fbq" || s.Command != cmd {
			t.Errorf("fbq consent %s: %+v", cmd, s)
		}
	}

	email := "visitor" + "@" + "example.test"
	s := hook(t, record(t, "fbq", "init", "123456789012", map[string]any{"em": email}))
	if s == nil || s.Source != "fbq" || s.Command != "init" {
		t.Fatalf("fbq init: %+v", s)
	}
	if strings.Contains(s.Raw, "12345678") || strings.Contains(s.Raw, email) || !strings.Contains(s.Raw, consent.MaskID("123456789012")) {
		t.Errorf("fbq init Raw not masked: %s", s.Raw)
	}
	if s := hook(t, record(t, "fbq", "init", 123456789012)); s == nil || strings.Contains(s.Raw, "12345678") {
		t.Errorf("numeric pixel ID: %+v", s)
	}
}

func TestMetaOrder(t *testing.T) {
	// browse fills AtMS from the record's time; the decoder keeps what
	// tells init and revoke apart.
	recs := []struct {
		t    float64
		args []any
	}{{30, []any{"consent", "revoke"}}, {10, []any{"init", "123456789012"}}, {20, []any{"track", "PageView"}}}
	var got []consent.Signal
	for _, r := range recs {
		if s := hook(t, record(t, "fbq", r.args...)); s != nil {
			s.AtMS = int64(r.t)
			got = append(got, *s)
		}
	}
	sort.Slice(got, func(i, j int) bool { return got[i].AtMS < got[j].AtMS })
	var cmds []string
	for _, s := range got {
		cmds = append(cmds, s.Command)
	}
	if !slices.Equal(cmds, []string{"init", "revoke"}) {
		t.Errorf("commands by time %v, want init then revoke", cmds)
	}
}

func TestUETCalls(t *testing.T) {
	s := hook(t, record(t, "uetq", "consent", "default", map[string]any{"ad_storage": "denied", "analytics_storage": "granted"}))
	if s == nil || s.Source != "uetq" || s.Command != "default" || len(s.State) != 1 || s.State["ad_storage"] != consent.Denied {
		t.Errorf("uetq default: %+v", s)
	}
	s = hook(t, record(t, "uetq", "consent", "update", map[string]any{"ad_storage": "granted"}))
	if s == nil || s.Command != "update" || s.State["ad_storage"] != consent.Granted {
		t.Errorf("uetq update: %+v", s)
	}
}

func TestCallsGarbage(t *testing.T) {
	tests := map[string][]byte{
		"fbq track":               record(t, "fbq", "track", "PageView"),
		"fbq numbers":             record(t, "fbq", 1, 2),
		"fbq nested arrays":       record(t, "fbq", []any{[]any{"consent"}}, []any{"revoke"}),
		"fbq consent other":       record(t, "fbq", "consent", "maybe"),
		"fbq consent alone":       record(t, "fbq", "consent"),
		"fbq init without id":     record(t, "fbq", "init"),
		"fbq init empty id":       record(t, "fbq", "init", ""),
		"fbq init object id":      record(t, "fbq", "init", map[string]any{"id": 1}),
		"uetq numbers":            record(t, "uetq", 1, 2, 3),
		"uetq nested":             record(t, "uetq", []any{"consent", "default", map[string]any{}}),
		"uetq event":              record(t, "uetq", "event", "purchase", map[string]any{}),
		"uetq consent no state":   record(t, "uetq", "consent", "default"),
		"uetq consent bad state":  record(t, "uetq", "consent", "default", []any{1}),
		"uetq consent other verb": record(t, "uetq", "consent", "revoke", map[string]any{}),
	}
	for name, rec := range tests {
		s, err := Decoder{}.Hook(rec)
		if s != nil || err != nil {
			t.Errorf("%s: got %+v, %v; want nil, nil", name, s, err)
		}
	}
}
