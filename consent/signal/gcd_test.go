// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package signal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/echo/consent"
)

// gcdRow is one row of testdata/gcd.yaml.
type gcdRow struct {
	Raw     string   `yaml:"raw"`
	Known   bool     `yaml:"known"`
	Signals []string `yaml:"signals"` // default/update, or unknown
	Extra   string   `yaml:"extra"`
	Note    string   `yaml:"note"`
	Source  string   `yaml:"source"`
	Date    string   `yaml:"date"`
}

func gcdRows(t testing.TB) []gcdRow {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "gcd.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var rows []gcdRow
	if err := yaml.Unmarshal(b, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("no gcd fixtures")
	}
	return rows
}

// gcdSignals lists a GCD's four signals in order.
func gcdSignals(g consent.GCD) []consent.GCDSignal {
	return []consent.GCDSignal{g.AdStorage, g.AnalyticsStorage, g.AdUserData, g.AdPersonalization}
}

func TestGCDFixtures(t *testing.T) {
	for _, row := range gcdRows(t) {
		got := Decoder{}.GCD(row.Raw)
		if got.Known != row.Known || got.Extra != row.Extra || got.Note != row.Note {
			t.Errorf("GCD(%q) known %v extra %q note %q, want %v %q %q", row.Raw, got.Known, got.Extra, got.Note, row.Known, row.Extra, row.Note)
		}
		if len(row.Signals) != 4 {
			t.Fatalf("row %q: want 4 signals", row.Raw)
		}
		for i, s := range gcdSignals(got) {
			want := row.Signals[i]
			if want == "unknown" {
				if s.Known || s.Default != consent.Unknown || s.Update != consent.Unknown {
					t.Errorf("GCD(%q) signal %d = %+v, want unknown", row.Raw, i, s)
				}
				continue
			}
			def, upd, _ := strings.Cut(want, "/")
			if !s.Known || string(s.Default) != def || string(s.Update) != upd || s.Letter != string(row.Raw[2+2*i]) {
				t.Errorf("GCD(%q) signal %d = %+v, want %s", row.Raw, i, s, want)
			}
		}
	}
}

func TestGCDExtraIsSafe(t *testing.T) {
	g := Decoder{}.GCD("13p3p3p3p5\x1b[31m" + strings.Repeat("a", 200))
	// Each control character is written as \xNN, so the cap is in input runes.
	if !g.Known || strings.ContainsRune(g.Extra, 0x1b) || !strings.HasSuffix(g.Extra, "…") || len([]rune(g.Extra)) > 4*maxExtra+1 {
		t.Errorf("Extra not made safe and capped: %q", g.Extra)
	}
}

func FuzzGCD(f *testing.F) {
	for _, row := range gcdRows(f) {
		f.Add(row.Raw)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		got := Decoder{}.GCD(raw)
		again := Decoder{}.GCD(raw)
		if got != again {
			t.Fatalf("GCD(%q) not deterministic", raw)
		}
		sigs := gcdSignals(got)
		all := true
		for i, s := range sigs {
			all = all && s.Known
			_, known := gcdLetters[byteAt(raw, 2+2*i)]
			if !known && (s.Known || s.Default == consent.Granted || s.Update == consent.Granted) {
				t.Fatalf("GCD(%q) signal %d from an unknown letter reads %+v", raw, i, s)
			}
			if !s.Known && (s.Default != consent.Unknown || s.Update != consent.Unknown) {
				t.Fatalf("GCD(%q) signal %d unknown with values %+v", raw, i, s)
			}
		}
		if got.Known != all {
			t.Fatalf("GCD(%q) known %v but signals known %v", raw, got.Known, all)
		}
		if gcdShape(raw) && got.Extra != consent.SafeText(raw[gcdLen:], maxExtra) {
			t.Fatalf("GCD(%q) extra %q not kept", raw, got.Extra)
		}
	})
}

// byteAt returns s[i], or 0 past the end.
func byteAt(s string, i int) byte {
	if i < len(s) {
		return s[i]
	}
	return 0
}
