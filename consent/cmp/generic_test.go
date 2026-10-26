// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cmp

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
)

var update = flag.Bool("update", false, "rewrite the generated generic language pages and their expect.yaml rows")

func TestGenericWords(t *testing.T) {
	lx, err := loadWords(genericWordsYAML)
	if err != nil {
		t.Fatal(err)
	}
	if len(genericLanguages) != 30 {
		t.Fatalf("%d languages; want 30", len(genericLanguages))
	}
	for c := classAccept; c <= classSave; c++ {
		if len(lx.phrases[c]) == 0 {
			t.Errorf("no %s phrases", c)
		}
	}
	for _, w := range []string{"cookie", "ملفات تعريف الارتباط", "עוגיות", "κουκιδ"} {
		if !slices.Contains(lx.scan.Cookie, w) {
			t.Errorf("cookie words lack %q: %v", w, lx.scan.Cookie)
		}
	}
	good := string(genericWordsYAML)
	for _, tc := range []struct{ from, to, want string }{
		{"cookie_words:", "cookie_word:", "field cookie_word not found"},
		{"  he:\n", "  xx:\n", "languages: he missing"},
		{"    accept: [قبول الكل, قبول, أوافق, موافق, السماح للكل, اقبل الكل, قبول جميع ملفات تعريف الارتباط]", "    accept: [قبول الكل]",
			"languages: ar: want at least three accept and three reject phrases"},
		{"    save: [kaydet,", "    save: [reddet, kaydet,", `"reddet" is both`},
		{"languages:\n", "languages:\n  xx: {accept: [a, b, c], reject: [d, e, f]}\n", "languages: xx isn't one of the 30"},
	} {
		bad := strings.Replace(good, tc.from, tc.to, 1)
		if bad == good {
			t.Fatalf("%q isn't in generic_words.yaml", tc.from)
		}
		if _, err := loadWords([]byte(bad)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s -> %s: err = %v; want %q", tc.from, tc.to, err, tc.want)
		}
	}
}

func TestNormText(t *testing.T) {
	for in, want := range map[string]string{
		"Alle\u00a0Akzeptieren":  "alle akzeptieren",
		"  ACCEPT\tALL ":         "accept all",
		"Ac\u200bcept all\u00ad": "accept all",
		"Accept all →":           "accept all",
		"ＡＣＣＥＰＴ ＡＬＬ":             "accept all",
		"ΑΠΟΔΟΧΗ ΟΛΩΝ":           "αποδοχη ολων",
		"Αποδοχή όλων":           "αποδοχη ολων",
		"Refuză tot":             "refuza tot",
		"Refuza\u0306 tot":       "refuza tot",
		"J'accepte":              "j accepte",
		"Cookie-Einstellungen":   "cookie einstellungen",
		"ملفاتُ تعريفِ الارتباط": "ملفات تعريف الارتباط",
		"":             "",
		"\u200b\u200c": "",
		"κουκίδες":     "κουκιδεσ",
	} {
		if got := normText(in); got != want {
			t.Errorf("normText(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestClassify(t *testing.T) {
	for _, tc := range []struct {
		text string
		want btnClass
	}{
		{"Accept all", classAccept},
		{"ACCEPT ALL COOKIES", classAccept},
		{"Allow all", classAccept},
		{"I agree", classAccept},
		{"Reject all", classReject},
		{"Decline", classReject},
		{"I disagree", classReject},
		{"Accept only necessary", classReject},
		{"Only necessary cookies", classReject},
		{"Use necessary cookies only", classReject},
		{"Cookie settings", classSettings},
		{"Settings", classSettings},
		{"Save my choices", classSave},
		{"Accept selection", classSave},
		{"Alle\u00a0akzeptieren", classAccept},
		{"Nur notwendige Cookies akzeptieren", classReject},
		{"Alle ablehnen", classReject},
		{"Auswahl speichern", classSave},
		{"Tout accepter", classAccept},
		{"Continuer sans accepter →", classReject},
		{"Tout refuser", classReject},
		{"Accetta tutto", classAccept},
		{"Rifiuta tutto", classReject},
		{"Aceptar todo", classAccept},
		{"Rechazar todo", classReject},
		{"Akceptuję", classAccept},
		{"Odrzuć wszystkie", classReject},
		{"Nie zgadzam się", classReject},
		{"Zgadzam się", classAccept},
		{"Kabul et", classAccept},
		{"Kabul etmiyorum", classReject},
		{"ΑΠΟΔΟΧΗ ΟΛΩΝ", classAccept},
		{"قبول الكل", classAccept},
		{"لا أوافق", classReject},
		{"קבל הכל", classAccept},
		{"אני לא מסכים", classReject},
		{"Niet akkoord", classReject},
		{"En hyväksy", classReject},
		{"Continue", classNone},
		{"Close", classNone},
		{"OK", classNone},
		{"Got it", classNone},
		{"×", classNone},
		{"X", classNone},
		{"Learn more", classNone},
		{"Privacy policy", classNone},
		{"Accept or reject", classNone},
		{"By clicking accept you agree to the storing of cookies on your device to enhance site navigation and measure usage", classNone},
		{"", classNone},
	} {
		if got, _ := words.classify(tc.text); got != tc.want {
			t.Errorf("classify(%q) = %s; want %s", tc.text, got, tc.want)
		}
	}
}

func TestContinueIsNotAccept(t *testing.T) {
	// gdpr-cookie-scanner clicked "Continuer sans accepter" as an Accept
	// (users.md 2.4): it holds "accepter", yet it refuses.
	for _, s := range []string{"Continuer sans accepter", "CONTINUER SANS ACCEPTER", "Continuer\u00a0sans accepter →", "continuer sans\u200b accepter"} {
		if got, _ := words.classify(s); got != classReject {
			t.Errorf("classify(%q) = %s; want reject", s, got)
		}
	}
	if got, _ := words.classify("Continuer"); got != classNone {
		t.Errorf("classify(Continuer) = %s; want none", got)
	}
}

func TestGenericContainer(t *testing.T) {
	bar := cookieBar("Accept all", "Reject all")
	if !bar.banner() || !bar.choice() {
		t.Fatal("a cookie bar with Accept and Reject isn't a banner with a choice")
	}
	if b, ok := bar.best(classReject); !ok || b.Text != "Reject all" {
		t.Fatalf("best reject = %+v", b)
	}
	// The more specific phrase wins: "Accept all cookies" over "Accept".
	two := cookieBar("Accept", "Accept all cookies")
	if b, _ := two.best(classAccept); b.Text != "Accept all cookies" {
		t.Errorf("best accept = %q", b.Text)
	}
	login := genContainer{Consent: true, Ignored: true, Buttons: cookieBar("I agree", "Decline").Buttons}
	if login.banner() || login.choice() {
		t.Error("a login dialog that asks for consent was taken for a banner")
	}
	cart := genContainer{Cookie: true, Ignored: true, Buttons: cookieBar("Checkout", "Continue shopping").Buttons}
	if !cart.banner() || cart.choice() {
		t.Error("a cart drawer that names cookies: a banner without a choice")
	}
	for name, change := range map[string]func(*genButton){
		"policy link": func(b *genButton) { b.Policy = true },
		"leaves":      func(b *genButton) { b.Leaves = true },
		"disabled":    func(b *genButton) { b.Disabled = true },
		"covered":     func(b *genButton) { b.Top = false },
		"hidden":      func(b *genButton) { b.Visible = false },
		"not a kind":  func(b *genButton) { b.Kind = false },
	} {
		c := cookieBar("Reject all")
		change(&c.Buttons[0])
		if _, ok := c.best(classReject); ok || c.choice() {
			t.Errorf("%s: the control would be clicked", name)
		}
	}
}

// genericSees is the generic script's report of a page, for the FakeTab
// tests.
type genericSees struct {
	Cookie    bool     `yaml:"cookie"`
	Consent   bool     `yaml:"consent"`
	Ignored   bool     `yaml:"ignored"`
	Buttons   []string `yaml:"buttons"`    // controls, by text
	Links     []string `yaml:"links"`      // links that load a policy page, by text
	Settings  []string `yaml:"settings"`   // controls of the settings dialog
	Toggles   []bool   `yaml:"toggles"`    // its switches when it opens
	ComesBack bool     `yaml:"comes_back"` // the banner shows again after a reload
}

// genericPage builds the fake page of one generic expect.yaml row.
func genericPage(d *driver, row expectPage) *fakePage {
	pg := newFakePage()
	pg.drive(d)
	g := row.Sees.Generic
	if g == nil {
		return pg
	}
	c := cookieBar(g.Buttons...)
	c.Cookie, c.Consent, c.Ignored = g.Cookie, g.Consent, g.Ignored
	for i, s := range g.Links {
		c.Buttons = append(c.Buttons, genButton{Path: fmt.Sprintf("#bar > a:nth-of-type(%d)", i+1), Text: s, Visible: true, Kind: true, Top: true, Leaves: true, Policy: true})
	}
	show := func(pg *fakePage) {
		pg.generic = []genContainer{c}
		for _, b := range c.Buttons {
			pg.show(b.Path, b.Text) // what the probe sees before a click
		}
		pg.sync()
	}
	if row.BannerAfterMS > 0 {
		pg.at(row.BannerAfterMS, show)
	} else {
		show(pg)
	}
	return pg
}

// genericClick plays the page's part: Accept, Reject and Save close the
// banner, the settings button opens a dialog with its switches, a
// switch flips.
func genericClick(pg *fakePage, g *genericSees) func(*consenttest.FakeTab, consent.Locator) {
	return func(_ *consenttest.FakeTab, l consent.Locator) {
		if strings.HasPrefix(l.CSS, "#toggle-") {
			var i int
			_, _ = fmt.Sscanf(l.CSS, "#toggle-%d", &i)
			pg.toggles[i].On = !pg.toggles[i].On
			pg.sync()
			return
		}
		var text string
		for _, c := range pg.generic {
			for _, b := range c.Buttons {
				if b.Path == l.CSS {
					text = b.Text
				}
			}
		}
		switch cl, _ := words.classify(text); cl {
		case classAccept, classReject, classSave:
			pg.generic = nil
		case classSettings:
			dialog := cookieBar(g.Settings...)
			dialog.Path = "#settings"
			for i := range dialog.Buttons {
				dialog.Buttons[i].Path = fmt.Sprintf("#settings > button:nth-of-type(%d)", i+1)
			}
			pg.generic = append(pg.generic, dialog)
			for _, b := range dialog.Buttons {
				pg.show(b.Path, b.Text)
			}
			pg.toggles = nil
			for i, on := range g.Toggles {
				pg.toggles = append(pg.toggles, toggle{Path: fmt.Sprintf("#toggle-%d", i), On: on})
			}
		}
		pg.sync()
	}
}

func readGenericExpect(t *testing.T) []expectPage {
	t.Helper()
	ex := readExpect(t, genericID)
	if len(ex.Pages) < 30+10 {
		t.Fatalf("generic/expect.yaml has %d rows; want the hand pages and 30 languages", len(ex.Pages))
	}
	return ex.Pages
}

func TestGenericReplicas(t *testing.T) {
	d, err := load(rulesFS, "rules")
	if err != nil {
		t.Fatal(err)
	}
	gen := consent.CMPInfo{ID: genericID, Name: d.byID[genericID].Name, Found: true, Present: true, DetectedBy: "generic"}
	for _, row := range readGenericExpect(t) {
		name := "generic/" + row.Page
		pg := genericPage(d, row)
		info, err := d.Detect(context.Background(), pg.tab, consenttest.NewPlan(t), 10*time.Second)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if info.Found != row.Found || info.Present != row.Present || info.DetectedBy != row.DetectedBy || (row.Found && info.ID != genericID) {
			t.Errorf("%s: detect %+v; want found %v by %q", name, info, row.Found, row.DetectedBy)
		}
		if row.BannerAfterMS > 0 {
			if early, _ := d.Detect(context.Background(), genericPage(d, row).tab, consenttest.NewPlan(t), 2*time.Second); early.Found {
				t.Errorf("%s: found within 2 s", name)
			}
		}
		if !row.Found {
			// Even when told the banner is generic, nothing is a choice.
			p := consenttest.NewPlan(t)
			p.BannerWait = 2 * time.Second
			for _, k := range []consent.RunKind{consent.RunAccept, consent.RunReject} {
				if ch, reason, err := d.Choose(context.Background(), pg.tab, p, gen, k); err != nil || reason != consent.ReasonNoButton || ch != nil {
					t.Errorf("%s %s: choice %+v, %q, %v; want no_button", name, k, ch, reason, err)
				}
			}
			if len(pg.tab.Clicked) != 0 {
				t.Errorf("%s: clicked %v", name, clicked(pg))
			}
			continue
		}
		for _, k := range []consent.RunKind{consent.RunAccept, consent.RunReject} {
			kname := name + " " + string(k)
			pg := genericPage(d, row)
			p := consenttest.NewPlan(t)
			info, _ := d.Detect(context.Background(), pg.tab, p, p.BannerWait)
			wantText, wantLayer := row.AcceptText, row.AcceptLayer
			if k == consent.RunReject {
				wantText, wantLayer = row.RejectText, row.RejectLayer
			}
			found, reason, err := d.Find(context.Background(), pg.tab, p, info, k)
			if err != nil || reason != consent.ReasonNone || found.Layer != wantLayer || normText(found.Text) != normText(wantText) {
				t.Errorf("%s: Find %+v, %q, %v; want %q at layer %d", kname, found, reason, err, wantText, wantLayer)
				continue
			}
			if len(pg.tab.Clicked) != 0 {
				t.Errorf("%s: Find clicked", kname)
			}
			g := row.Sees.Generic
			pg.tab.OnClick = genericClick(pg, g)
			if g.ComesBack && k == consent.RunReject {
				// Its Reject only hides the banner: nothing is stored.
				shown := slices.Clone(pg.generic)
				pg.onReload = func(pg *fakePage) {
					pg.generic = shown
					pg.sync()
				}
			}
			ch, reason, err := d.Choose(context.Background(), pg.tab, p, info, k)
			if err != nil {
				t.Fatalf("%s: %v", kname, err)
			}
			if pg.tab.Reloads == 0 {
				t.Errorf("%s: no reload to check the banner stays gone", kname)
			}
			if k == consent.RunReject && row.RejectReason != "" {
				if string(reason) != row.RejectReason || ch.Verified {
					t.Errorf("%s: choice %+v, %q; want %s", kname, ch, reason, row.RejectReason)
				}
				continue
			}
			if reason != consent.ReasonNone || !ch.Verified || ch.VerifiedBy != "banner" || ch.Layer != wantLayer {
				t.Errorf("%s: choice %+v, %q; want verified by banner at layer %d", kname, ch, reason, wantLayer)
			}
			if got := clicked(pg); len(got) == 0 || !strings.HasPrefix(ch.Selector, got[0]) || !strings.HasPrefix(ch.Selector, found.Selector) {
				t.Errorf("%s: Find said %q, Choose clicked %v", kname, found.Selector, got)
			}
			for _, c := range clicked(pg) {
				if strings.Contains(c, " > a:") {
					t.Errorf("%s: clicked the link %s", kname, c)
				}
			}
			if wantLayer == 2 {
				for _, tg := range pg.toggles {
					if tg.On {
						t.Errorf("%s: a switch was left on: %+v", kname, pg.toggles)
					}
				}
			}
		}
	}
}

func TestGenericNeverHides(t *testing.T) {
	b, err := os.ReadFile("js/generic.js")
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(string(b), "\n") {
		if m := pageWrites.FindString(line); m != "" && !strings.HasPrefix(strings.TrimSpace(line), "//") {
			t.Errorf("js/generic.js:%d writes to the page (%q)", i+1, m)
		}
	}
}

// cookieNoun is the word for cookies each language page's text uses.
var cookieNoun = map[string]string{
	"bg": "бисквитки", "cs": "soubory cookie", "el": "κουκίδες", "et": "küpsised", "fi": "evästeitä", "ga": "fianáin", "hr": "kolačiće",
	"hu": "sütiket", "is": "vafrakökur", "lt": "slapukus", "lv": "sīkdatnes", "no": "informasjonskapsler", "sl": "piškotke", "tr": "çerezler",
	"uk": "файли cookie", "ar": "ملفات تعريف الارتباط", "he": "עוגיות",
}

// titled writes a phrase the way a button shows it: first letter upper
// case.
func titled(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[n:]
}

// langPage is the generated page of one language: a cookie bar with its
// first settings, reject and accept phrases.
func langPage(code string, lw langWords) string {
	noun := cookieNoun[code]
	if noun == "" {
		noun = "cookies"
	}
	dir := ""
	if code == "ar" || code == "he" {
		dir = ` dir="rtl"`
	}
	return fmt.Sprintf(`<!doctype html>
<!-- Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
     SPDX-License-Identifier: MIT -->
<!-- Generated by TestGenericLanguagePages from generic_words.yaml (%s):
     a hand-made cookie bar for Echo's generic rule tests. Do not edit. -->
<html lang="%s"%s>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Corner Shop (%s)</title>
<style>
  body { font: 16px/1.5 sans-serif; margin: 0; padding: 24px; }
  [hidden] { display: none !important; }
  .bar { position: fixed; left: 0; right: 0; bottom: 0; z-index: 9999; background: #fff; border-top: 2px solid #333; padding: 16px 24px; }
  .bar button { margin: 4px 8px 4px 0; padding: 8px 16px; font-size: 15px; }
</style>
</head>
<body>
<h1>Corner Shop</h1>
<template id="replica-banner">
<div class="bar" data-banner role="region" aria-label="%s">
  <p>%s</p>
  <button type="button" data-do="settings">%s</button>
  <button type="button" data-do="reject">%s</button>
  <button type="button" data-do="accept">%s</button>
</div>
</template>
<script src="banner.js" data-show="now"></script>
</body>
</html>
`, code, code, dir, code, noun, noun, titled(lw.Settings[0]), titled(lw.Reject[0]), titled(lw.Accept[0]))
}

// langRow is the expect.yaml row of a language page.
func langRow(code string, lw langWords) string {
	return fmt.Sprintf(`  - page: lang-%s.html
    found: true
    present: true
    detected_by: "generic"
    accept_layer: 1
    reject_layer: 1
    accept_text: %q
    reject_text: %q
    sees:
      generic:
        cookie: true
        buttons: [%q, %q, %q]
`, code, titled(lw.Accept[0]), titled(lw.Reject[0]), titled(lw.Settings[0]), titled(lw.Reject[0]), titled(lw.Accept[0]))
}

const generatedMark = "# generated by TestGenericLanguagePages\n"

func TestGenericLanguagePages(t *testing.T) {
	var f wordsFile
	if err := yaml.Unmarshal(genericWordsYAML, &f); err != nil {
		t.Fatal(err)
	}
	var rows strings.Builder
	for _, code := range genericLanguages {
		lw := f.Languages[code]
		if c, _ := words.classify(titled(lw.Accept[0])); c != classAccept {
			t.Errorf("%s: %q reads as %s", code, lw.Accept[0], c)
		}
		if c, _ := words.classify(titled(lw.Reject[0])); c != classReject {
			t.Errorf("%s: %q reads as %s", code, lw.Reject[0], c)
		}
		if c, _ := words.classify(titled(lw.Settings[0])); c != classSettings {
			t.Errorf("%s: %q reads as %s", code, lw.Settings[0], c)
		}
		path := filepath.Join("testdata", "generic", "lang-"+code+".html")
		page := langPage(code, lw)
		if *update {
			if err := os.WriteFile(path, []byte(page), 0o644); err != nil {
				t.Fatal(err)
			}
		} else if b, err := os.ReadFile(path); err != nil || string(b) != page {
			t.Errorf("%s is out of date: go test ./consent/cmp -run TestGenericLanguagePages -update", path)
		}
		rows.WriteString(langRow(code, lw))
	}
	expect := filepath.Join("testdata", "generic", "expect.yaml")
	b, err := os.ReadFile(expect)
	if err != nil {
		t.Fatal(err)
	}
	head, _, ok := bytes.Cut(b, []byte(generatedMark))
	if !ok {
		t.Fatalf("%s has no %q line", expect, strings.TrimSpace(generatedMark))
	}
	want := string(head) + generatedMark + rows.String()
	if *update {
		if err := os.WriteFile(expect, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
	} else if string(b) != want {
		t.Errorf("%s: the language rows are out of date: go test ./consent/cmp -run TestGenericLanguagePages -update", expect)
	}
}

func FuzzGenericButtonText(f *testing.F) {
	for _, s := range []string{"Accept all", "Continuer sans accepter", "Alle\u00a0akzeptieren", "Ac\u200bcept", "ＲＥＪＥＣＴ",
		"قبول الكل", "אני לא מסכים", "Accept или Отхвърли", "×", "OK", "\xff\xfe", strings.Repeat("a ", 100)} {
		f.Add(s)
	}
	never := make([]string, 0, len(words.never))
	for w := range words.never {
		never = append(never, w)
	}
	slices.Sort(never)
	f.Fuzz(func(t *testing.T, s string) {
		c1, n1 := words.classify(s)
		c2, n2 := words.classify(s)
		if c1 != c2 || n1 != n2 {
			t.Fatalf("classify(%q) not deterministic: %s/%d then %s/%d", s, c1, n1, c2, n2)
		}
		if c1 == classNone && n1 != 0 {
			t.Fatalf("classify(%q): none with a phrase length", s)
		}
		_ = normText(s)
		// A never_choice word stays no choice however it is written:
		// upper case, zero-width characters, non-breaking spaces.
		w := never[len(s)%len(never)]
		for _, v := range []string{strings.ToUpper(w), "\u200b" + w + "\u200d", strings.ReplaceAll(w, " ", "\u00a0"), " " + w + " "} {
			if c, _ := words.classify(v); c == classAccept || c == classReject {
				t.Fatalf("never_choice %q written %q classifies as %s", w, v, c)
			}
		}
	})
}
