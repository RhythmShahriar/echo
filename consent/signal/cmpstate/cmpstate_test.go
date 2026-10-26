// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cmpstate

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/echo/consent"
)

// row is one fixture: the values handed to the parser and the state it
// should return (nil: no state).
type row struct {
	Name       string            `yaml:"name"`
	Values     map[string]string `yaml:"values"`
	Unreadable bool              `yaml:"unreadable"`
	Want       *struct {
		Decided    bool            `yaml:"decided"`
		Source     string          `yaml:"source"`
		Categories map[string]bool `yaml:"categories"`
	} `yaml:"want"`
}

func rows(t testing.TB, cmp string) []row {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", cmp+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var out []row
	if err := yaml.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatalf("%s: no fixtures", cmp)
	}
	return out
}

func TestFixtures(t *testing.T) {
	for cmp, parse := range Parsers {
		t.Run(cmp, func(t *testing.T) {
			var kinds [3]bool // accepted, rejected, undecided seen
			for _, r := range rows(t, cmp) {
				got, err := parse(r.Values)
				switch {
				case strings.HasPrefix(r.Name, "accepted"):
					kinds[0] = true
				case strings.HasPrefix(r.Name, "rejected"):
					kinds[1] = true
				case strings.HasPrefix(r.Name, "undecided"):
					kinds[2] = true
				}
				if r.Unreadable != errors.Is(err, ErrUnreadable) || (err != nil && !r.Unreadable) {
					t.Errorf("%s: err %v, want unreadable %v", r.Name, err, r.Unreadable)
				}
				if r.Want == nil {
					if got != nil {
						t.Errorf("%s: %+v, want nil", r.Name, got)
					}
					continue
				}
				if got == nil {
					t.Fatalf("%s: nil, want %+v", r.Name, r.Want)
				}
				want := r.Want.Categories
				if len(want) == 0 {
					want = nil
				}
				if got.CMP != cmp || got.Decided != r.Want.Decided || got.Source != r.Want.Source || !maps.Equal(got.Categories, want) {
					t.Errorf("%s: %+v, want %+v", r.Name, got, r.Want)
				}
			}
			if kinds != [3]bool{true, true, true} {
				t.Errorf("fixtures need accepted, rejected and undecided rows: %v", kinds)
			}
		})
	}
}

// names is a value name each parser reads: its default, with a prefix
// filled in.
func names(cmp string) []string {
	d := Defaults[cmp]
	out := []string{}
	for _, n := range append(slices.Clone(d.Cookies), d.Storage...) {
		if p, ok := strings.CutSuffix(n, "*"); ok {
			n = p + "marketing"
			if cmp == "iubenda" {
				n = p + "12345"
			}
		}
		out = append(out, n)
	}
	return out
}

// hostile are values a page may write to break a parser.
func hostile() []string {
	return []string{
		strings.Repeat("A", 5<<10),
		strings.Repeat(`{"a":`, 100) + "1" + strings.Repeat("}", 100),
		strings.Repeat("{a:", 100) + "1" + strings.Repeat("}", 100),
		strings.Repeat("[", 100) + strings.Repeat("]", 100),
		"\x00\x01\x1b[31m‮\x7f",
		strings.Repeat("%25", 2000),
		strings.Repeat("x", MaxValue+1),
	}
}

func TestHostileValues(t *testing.T) {
	for cmp, parse := range Parsers {
		for _, name := range names(cmp) {
			for i, v := range hostile() {
				got, err := parse(map[string]string{name: v})
				if got != nil && got.Decided {
					t.Errorf("%s %s hostile %d: decided", cmp, name, i)
				}
				if err != nil && len(v) > 40 && strings.Contains(err.Error(), v[:40]) {
					t.Errorf("%s %s hostile %d: error carries the value", cmp, name, i)
				}
				if got != nil && len(got.Categories) > maxCategories {
					t.Errorf("%s: %d categories", cmp, len(got.Categories))
				}
			}
		}
	}
}

func TestCategoryCaps(t *testing.T) {
	var b strings.Builder
	b.WriteString("interactionCount=1&groups=")
	for i := range 300 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString("G" + strconv.Itoa(i) + ":1")
	}
	got, err := OneTrust(map[string]string{"OptanonConsent": b.String()})
	if err != nil || len(got.Categories) != maxCategories {
		t.Fatalf("%v, %d categories", err, len(got.Categories))
	}
	got, _ = OneTrust(map[string]string{"OptanonConsent": "groups=" + strings.Repeat("x", 500) + ":1"})
	for k := range got.Categories {
		if len([]rune(k)) != maxName+1 || !strings.HasSuffix(k, "…") {
			t.Errorf("category name of %d runes", len([]rune(k)))
		}
	}
	// Names are made safe to print.
	got, _ = CookieYes(map[string]string{"cookieyes-consent": "action:yes,\x1b[31mads:yes"})
	if _, ok := got.Categories[`\x1b[31mads`]; !ok {
		t.Errorf("unsafe name kept: %v", got.Categories)
	}
}

func TestTCF(t *testing.T) {
	decode := func(s string) (*consent.TCF, error) {
		if s == "good" {
			return &consent.TCF{Version: 2, PurposesConsent: []int{1}, Valid: true}, nil
		}
		return nil, errors.New("TC string too short")
	}
	p := TCF(decode)
	got, err := p(map[string]string{"euconsent-v2": "good"})
	if err != nil || !got.Decided || got.Source != "tcf" || got.CMP != "tcf" || got.TCF == nil {
		t.Errorf("good: %+v, %v", got, err)
	}
	got, err = p(map[string]string{"euconsent-v2": "bad"})
	if !errors.Is(err, ErrUnreadable) || got == nil || got.Decided || strings.Contains(err.Error(), "bad") {
		t.Errorf("bad: %+v, %v", got, err)
	}
	if got, err := p(map[string]string{}); got != nil || err != nil {
		t.Errorf("missing: %+v, %v", got, err)
	}
}

// fuzzParser checks one parser: never panics, deterministic, the caps
// hold, an error never carries a long value and a decided state needs
// the marker the format decides by (in some decoding of the value).
func fuzzParser(f *testing.F, cmp, name string, markers ...string) {
	for _, r := range rows(f, cmp) {
		if v, ok := r.Values[name]; ok {
			f.Add(v)
		}
	}
	for _, v := range hostile()[:5] {
		f.Add(v)
	}
	parse := Parsers[cmp]
	f.Fuzz(func(t *testing.T, v string) {
		values := map[string]string{name: v}
		got, err := parse(values)
		again, err2 := parse(values)
		if (err == nil) != (err2 == nil) || (got == nil) != (again == nil) {
			t.Fatalf("%s(%q) not deterministic", cmp, v)
		}
		if got == nil {
			return
		}
		if got.Decided != again.Decided || !maps.Equal(got.Categories, again.Categories) {
			t.Fatalf("%s(%q) not deterministic", cmp, v)
		}
		if len(got.Categories) > maxCategories {
			t.Fatalf("%s(%q): %d categories", cmp, v, len(got.Categories))
		}
		if err != nil && (got.Decided || len(v) >= 40 && strings.Contains(err.Error(), v)) {
			t.Fatalf("%s(%q): %+v, %v", cmp, v, got, err)
		}
		if got.Decided && len(markers) > 0 {
			for _, d := range decodings(v) {
				for _, m := range markers {
					if strings.Contains(d, m) {
						return
					}
				}
			}
			t.Fatalf("%s(%q) decided without %v", cmp, v, markers)
		}
	})
}

func FuzzCookiebot(f *testing.F) {
	fuzzParser(f, "cookiebot", "CookieConsent", "explicit", "implied", "-1")
}

func FuzzOneTrust(f *testing.F) { fuzzParser(f, "onetrust", "OptanonConsent", "interactionCount") }

func FuzzCookieYes(f *testing.F) { fuzzParser(f, "cookieyes", "cookieyes-consent", "action:yes") }

func FuzzComplianz(f *testing.F) { fuzzParser(f, "complianz", "cmplz_marketing", "allow", "deny") }

func FuzzUsercentrics(f *testing.F) { fuzzParser(f, "usercentrics", "uc_settings", "explicit") }

func FuzzBorlabs(f *testing.F) { fuzzParser(f, "borlabs", "borlabs-cookie", "uid") }

// FuzzDidomi has no text marker: the value is base64.
func FuzzDidomi(f *testing.F) { fuzzParser(f, "didomi", "didomi_token") }

func FuzzIubenda(f *testing.F) { fuzzParser(f, "iubenda", "_iub_cs-12345", "purposes") }

// FuzzCMPState feeds one value to every parser under every name it
// reads: none panics or decides on a hostile value.
func FuzzCMPState(f *testing.F) {
	for _, v := range hostile()[:5] {
		f.Add(v)
	}
	f.Add("{stamp:'x',method:'explicit'}")
	f.Fuzz(func(t *testing.T, v string) {
		for cmp, parse := range Parsers {
			for _, name := range names(cmp) {
				got, _ := parse(map[string]string{name: v})
				if got != nil && got.CMP != cmp {
					t.Fatalf("%s(%q) says CMP %q", cmp, v, got.CMP)
				}
			}
		}
	})
}
