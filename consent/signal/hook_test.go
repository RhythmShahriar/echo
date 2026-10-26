// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package signal

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
)

// record builds a hook record with these arguments.
func record(t testing.TB, src string, args ...any) []byte {
	t.Helper()
	raw := make([]json.RawMessage, len(args))
	for i, a := range args {
		b, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		raw[i] = b
	}
	b, err := json.Marshal(consent.HookRecord{T: 1, Src: src, Args: raw})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func hook(t *testing.T, rec []byte) *consent.Signal {
	t.Helper()
	s, err := Decoder{}.Hook(rec)
	if err != nil {
		t.Fatalf("Hook(%s): %v", rec, err)
	}
	return s
}

func TestHookConsentMode(t *testing.T) {
	g, d, x := consent.Granted, consent.Denied, consent.Unknown
	allDenied := map[string]any{"ad_storage": "denied", "analytics_storage": "denied", "ad_user_data": "denied",
		"ad_personalization": "denied", "wait_for_update": 500}
	tests := []struct {
		name  string
		rec   []byte
		src   string
		cmd   string
		state map[string]consent.Value
		wait  int
	}{
		{"gtag default all denied", record(t, "gtag", "consent", "default", allDenied), "gtag", "default",
			map[string]consent.Value{"ad_storage": d, "analytics_storage": d, "ad_user_data": d, "ad_personalization": d}, 500},
		{"dataLayer array push", record(t, "datalayer", []any{"consent", "update", map[string]any{"ad_storage": "granted", "analytics_storage": "granted"}}),
			"datalayer", "update", map[string]consent.Value{"ad_storage": g, "analytics_storage": g}, 0},
		{"Arguments object", record(t, "datalayer", map[string]any{"0": "consent", "1": "default", "2": map[string]any{"analytics_storage": "denied"}}),
			"datalayer", "default", map[string]consent.Value{"analytics_storage": d}, 0},
		{"unknown value", record(t, "gtag", "consent", "update", map[string]any{"ad_storage": "maybe", "analytics_storage": true}),
			"gtag", "update", map[string]consent.Value{"ad_storage": x, "analytics_storage": x}, 0},
		{"all seven keys and no others", record(t, "gtag", "consent", "default", map[string]any{"functionality_storage": "granted",
			"personalization_storage": "denied", "security_storage": "granted", "other_storage": "granted", "region": []string{"DE"}}),
			"gtag", "default", map[string]consent.Value{"functionality_storage": g, "personalization_storage": d, "security_storage": g}, 0},
		{"wait clamped high", record(t, "gtag", "consent", "default", map[string]any{"wait_for_update": 1e12}), "gtag", "default", map[string]consent.Value{}, maxWaitMS},
		{"wait clamped low", record(t, "gtag", "consent", "default", map[string]any{"wait_for_update": -5}), "gtag", "default", map[string]consent.Value{}, 0},
		{"wait not a number", record(t, "gtag", "consent", "default", map[string]any{"wait_for_update": "500"}), "gtag", "default", map[string]consent.Value{}, 0},
		{"set ads_data_redaction", record(t, "gtag", "set", "ads_data_redaction", true), "gtag", "set", nil, 0},
		{"set url_passthrough", record(t, "datalayer", []any{"set", "url_passthrough", true}), "datalayer", "set", nil, 0},
		{"set object form", record(t, "gtag", "set", map[string]any{"ads_data_redaction": true}), "gtag", "set", nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := hook(t, tt.rec)
			if s == nil {
				t.Fatal("got nil")
			}
			if s.Source != tt.src || s.Command != tt.cmd || s.WaitForUpdateMS != tt.wait || s.AtMS != 0 {
				t.Errorf("got %s %s wait %d at %d", s.Source, s.Command, s.WaitForUpdateMS, s.AtMS)
			}
			if tt.state != nil && !maps.Equal(s.State, tt.state) {
				t.Errorf("state %v, want %v", s.State, tt.state)
			}
			if s.Raw == "" {
				t.Error("Raw is empty")
			}
		})
	}
}

func TestHookNotConsentMode(t *testing.T) {
	tests := map[string][]byte{
		"event consent":               record(t, "datalayer", map[string]any{"event": "consent", "ad_storage": "granted"}),
		"event cookie_consent_update": record(t, "datalayer", map[string]any{"event": "cookie_consent_update"}),
		"gtag config":                 record(t, "gtag", "config", "G-TEST"),
		"gtag event":                  record(t, "gtag", "event", "page_view", map[string]any{}),
		"consent without state":       record(t, "gtag", "consent", "default"),
		"consent state not an object": record(t, "gtag", "consent", "default", "denied"),
		"consent unknown command":     record(t, "gtag", "consent", "revoke", map[string]any{}),
		"set other key":               record(t, "gtag", "set", "developer_id", true),
		"numbers":                     record(t, "gtag", 1, 2, 3),
		"no args":                     record(t, "gtag"),
		"click":                       record(t, "click", "consent", "default", map[string]any{}),
		"unknown src":                 record(t, "other", "consent", "default", map[string]any{}),
		"Arguments object with a gap": record(t, "datalayer", map[string]any{"0": "consent", "2": map[string]any{}}),
	}
	for name, rec := range tests {
		if s := hook(t, rec); s != nil {
			t.Errorf("%s: got %+v, want nil", name, s)
		}
	}
}

func TestHookRegion(t *testing.T) {
	global := hook(t, record(t, "gtag", "consent", "default", map[string]any{"ad_storage": "granted"}))
	scoped := hook(t, record(t, "gtag", "consent", "default", map[string]any{"ad_storage": "denied", "region": []string{"DE", "FR"}}))
	if !slices.Equal(scoped.Region, []string{"DE", "FR"}) || global.Region != nil {
		t.Fatalf("regions %v and %v", scoped.Region, global.Region)
	}
	// The caller fills AtMS on the run clock; the regional default came first.
	scoped.AtMS, global.AtMS = 10, 20
	pair := []consent.Signal{*scoped, *global}
	if st, at, ok := consent.EffectiveDefault(pair, "de"); !ok || at != 10 || st["ad_storage"] != consent.Denied {
		t.Errorf("de: %v at %d ok %v, want the regional default", st, at, ok)
	}
	if st, at, ok := consent.EffectiveDefault(pair, "us"); !ok || at != 20 || st["ad_storage"] != consent.Granted {
		t.Errorf("us: %v at %d ok %v, want the global default", st, at, ok)
	}
}

func TestHookRegionLimits(t *testing.T) {
	list := []any{"US-CA", "EU-LONGER-THAN-10", "<b>x</b>", 7, "", "de"}
	for range 400 {
		list = append(list, "GB")
	}
	s := hook(t, record(t, "gtag", "consent", "default", map[string]any{"region": list}))
	if len(s.Region) != maxRegions || s.Region[0] != "US-CA" || s.Region[1] != "de" {
		t.Errorf("region kept %d entries starting %v", len(s.Region), s.Region[:2])
	}
}

func TestHookLimits(t *testing.T) {
	big := record(t, "gtag", "consent", "default", map[string]any{"ad_storage": strings.Repeat("x", 5<<10)})
	if _, err := (Decoder{}).Hook(big); err == nil || err.Error() != "hook record too large" {
		t.Errorf("5 KiB record: %v", err)
	}
	deep := `{"src":"gtag","args":["consent","default",` + strings.Repeat("[", 20) + strings.Repeat("]", 20) + `]}`
	if _, err := (Decoder{}).Hook([]byte(deep)); err == nil {
		t.Error("a record nested 22 deep was accepted")
	}
	if _, err := (Decoder{}).Hook([]byte(`{"src":`)); err == nil {
		t.Error("broken JSON was accepted")
	}
	// Brackets inside strings don't count as depth.
	str := `{"src":"gtag","args":["consent","default",{"ad_storage":"denied","x":"` + strings.Repeat("[", 50) + `"}]}`
	if s := hook(t, []byte(str)); s == nil || s.State["ad_storage"] != consent.Denied {
		t.Errorf("brackets in a string: %+v", s)
	}
}

func TestHookRawMaskedAndCut(t *testing.T) {
	// A fake key assembled at runtime: push protection blocks literal ones.
	key := "sk_" + "live_" + "4eC39HqLyjWD" + "arjtT1zdp7dc"
	s := hook(t, record(t, "gtag", "consent", "update", map[string]any{"ad_storage": "granted", "note": key}))
	if strings.Contains(s.Raw, key) {
		t.Errorf("Raw keeps the key: %s", s.Raw)
	}
	if strings.HasSuffix(s.Raw, cutSuffix) {
		t.Errorf("Raw marked cut: %s", s.Raw)
	}

	var h consent.HookRecord
	if err := json.Unmarshal(record(t, "gtag", "consent", "update", map[string]any{"ad_storage": "denied"}), &h); err != nil {
		t.Fatal(err)
	}
	h.Cut = true
	b, _ := json.Marshal(h)
	s = hook(t, b)
	if !strings.HasSuffix(s.Raw, " (cut)") || s.State["ad_storage"] != consent.Denied {
		t.Errorf("cut record: %+v", s)
	}

	// Bidi and other controls in page text are escaped.
	s = hook(t, record(t, "gtag", "consent", "update", map[string]any{"ad_storage": "denied", "x": "a\u202eb"}))
	if strings.ContainsRune(s.Raw, '\u202e') {
		t.Errorf("Raw keeps a bidi override: %q", s.Raw)
	}
}

func TestCutRaw(t *testing.T) {
	long := strings.Repeat("é", consent.MaxHookRecord)
	got := cutRaw(long, false)
	if len(got) > consent.MaxHookRecord || !strings.HasSuffix(got, cutSuffix) || !strings.HasPrefix(got, "é") {
		t.Errorf("cutRaw kept %d bytes", len(got))
	}
	if got := cutRaw("abc", false); got != "abc" {
		t.Errorf("cutRaw(abc) = %q", got)
	}
}

func FuzzHookEvent(f *testing.F) {
	f.Add(record(f, "gtag", "consent", "default", map[string]any{"ad_storage": "denied", "region": []string{"DE"}, "wait_for_update": 500}))
	f.Add(record(f, "datalayer", map[string]any{"0": "consent", "1": "update", "2": map[string]any{"ad_storage": "granted"}}))
	f.Add(record(f, "datalayer", []any{"consent", "default", map[string]any{"analytics_storage": "denied"}}))
	f.Add(record(f, "datalayer", map[string]any{"event": "consent"}))
	f.Add(record(f, "fbq", "init", "1234567890", map[string]any{"em": "a@b.example"}))
	f.Add(record(f, "fbq", "consent", "revoke"))
	f.Add(record(f, "uetq", "consent", "default", map[string]any{"ad_storage": "denied"}))
	f.Add([]byte(`{"src":"gtag","args":[[[[[[[[[[[]]]]]]]]]]]}`))
	f.Add([]byte(`{"src":"gtag","cut":true,"args":["consent","default",{"ad_storage":"granted"}]}`))
	allowed := map[string]bool{}
	for _, k := range consentKeys {
		allowed[k] = true
	}
	f.Fuzz(func(t *testing.T, rec []byte) {
		s, err := Decoder{}.Hook(rec)
		s2, err2 := Decoder{}.Hook(rec)
		if (err == nil) != (err2 == nil) || (s == nil) != (s2 == nil) || (s != nil && s.Raw != s2.Raw) {
			t.Fatal("Hook not deterministic")
		}
		if len(rec) > consent.MaxHookRecord && err == nil {
			t.Fatal("record over the size limit accepted")
		}
		if depth(rec) > maxDepth && err == nil {
			t.Fatal("record over the depth limit accepted")
		}
		if s == nil {
			return
		}
		if err != nil {
			t.Fatal("signal and error both returned")
		}
		for k := range s.State {
			if !allowed[k] {
				t.Fatalf("state key %q", k)
			}
		}
		if len(s.Raw) > consent.MaxHookRecord || len(s.Region) > maxRegions || s.WaitForUpdateMS < 0 || s.WaitForUpdateMS > maxWaitMS {
			t.Fatalf("limits broken: raw %d, regions %d, wait %d", len(s.Raw), len(s.Region), s.WaitForUpdateMS)
		}
		for _, r := range s.Region {
			if len(r) > maxRegionLen {
				t.Fatalf("region %q too long", r)
			}
		}
	})
}

// TestWaitMS checks the clamp holds past float64's range and that what
// isn't a number reads as no wait.
func TestWaitMS(t *testing.T) {
	tests := []struct {
		raw  string
		want int
	}{
		{"500", 500},
		{"1e400", maxWaitMS},
		{"1e12", maxWaitMS},
		{"-1e400", 0},
		{"-5", 0},
		{" 250 ", 250},
		{`"500"`, 0},
		{"null", 0},
		{"", 0},
		{"true", 0},
		{"{}", 0},
	}
	for _, tt := range tests {
		if got := waitMS(json.RawMessage(tt.raw)); got != tt.want {
			t.Errorf("waitMS(%q) = %d, want %d", tt.raw, got, tt.want)
		}
	}
	if got := waitMS(nil); got != 0 {
		t.Errorf("waitMS(nil) = %d", got)
	}
}
