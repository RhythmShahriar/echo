// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package signal

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	// GCS and GCSSignals are the gcs from the same hit and what it reads
	// (ad_storage, analytics_storage). Captured rows carry them; built
	// rows may leave them out.
	GCS        string   `yaml:"gcs"`
	GCSSignals []string `yaml:"gcs_signals"`
	Source     string   `yaml:"source"`
	Date       string   `yaml:"date"`
}

// captureSource is the source of a row copied from a real gtag.js hit
// (testdata/captures/README.md). Such a row must carry the hit's gcs.
const captureSource = "capture"

// rowProvenance says what is missing from a row's source and date: every
// row says where its value came from and when, so a stale or invented
// fixture can be told from a real hit.
func rowProvenance(row gcdRow) error {
	if strings.TrimSpace(row.Source) == "" {
		return errors.New("no source")
	}
	d, err := time.Parse(time.DateOnly, row.Date)
	if err != nil {
		return fmt.Errorf("date %q is not YYYY-MM-DD", row.Date)
	}
	if d.Before(time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)) || d.After(time.Now().AddDate(0, 0, 1)) {
		// gcd appeared with Consent Mode v2 tooling; anything before 2023
		// or in the future is a typo.
		return fmt.Errorf("date %s out of range", row.Date)
	}
	if strings.HasPrefix(row.Source, captureSource) {
		if row.GCS == "" || len(row.GCSSignals) != 2 {
			return errors.New("capture row without gcs and gcs_signals")
		}
		if !row.Known {
			// A real hit that doesn't decode is a finding to fix in the
			// letter table, not a fixture to keep as unknown.
			return errors.New("capture row that does not decode")
		}
	}
	return nil
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
		if row.GCS != "" {
			gcs := Decoder{}.GCS(row.GCS)
			if len(row.GCSSignals) != 2 || !gcs.Known || string(gcs.AdStorage) != row.GCSSignals[0] || string(gcs.AnalyticsStorage) != row.GCSSignals[1] {
				t.Errorf("row %q: GCS(%q) = %+v, want %v", row.Raw, row.GCS, gcs, row.GCSSignals)
			}
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

func TestFixtureRowsHaveSource(t *testing.T) {
	for _, row := range gcdRows(t) {
		if err := rowProvenance(row); err != nil {
			t.Errorf("gcd.yaml row %q: %v", row.Raw, err)
		}
	}
	// Negatives: rows the check must refuse.
	good := gcdRow{Raw: "13p3p3p3p5", Known: true, Source: "public example", Date: "2026-10-26"}
	capture := gcdRow{Raw: "13p3p3p3p5", Known: true, GCS: "G100", GCSSignals: []string{"denied", "denied"}, Source: "capture: reject", Date: "2026-10-26"}
	if err := rowProvenance(good); err != nil {
		t.Fatalf("good row refused: %v", err)
	}
	if err := rowProvenance(capture); err != nil {
		t.Fatalf("capture row refused: %v", err)
	}
	bad := map[string]func(*gcdRow){
		"no source":         func(r *gcdRow) { r.Source = " " },
		"no date":           func(r *gcdRow) { r.Date = "" },
		"wrong date format": func(r *gcdRow) { r.Date = "26/10/2026" },
		"too old":           func(r *gcdRow) { r.Date = "2019-01-01" },
		"future":            func(r *gcdRow) { r.Date = time.Now().AddDate(1, 0, 0).Format(time.DateOnly) },
		"capture no gcs":    func(r *gcdRow) { r.Source, r.GCS, r.GCSSignals = "capture: accept", "", nil },
		"capture unknown":   func(r *gcdRow) { r.Known = false },
	}
	for name, edit := range bad {
		row := capture
		edit(&row)
		if rowProvenance(row) == nil {
			t.Errorf("%s: row accepted", name)
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
