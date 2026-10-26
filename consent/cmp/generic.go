// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cmp

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/echo/consent"
)

// genericWordsYAML holds the generic rule's words (see the file).
//
//go:embed generic_words.yaml
var genericWordsYAML []byte

// genericLanguages are the languages the generic rule reads: the 24 EU
// languages plus Norwegian, Icelandic, Turkish, Ukrainian, Arabic and
// Hebrew.
var genericLanguages = []string{"bg", "cs", "da", "de", "el", "en", "es", "et", "fi", "fr", "ga", "hr", "hu", "it", "lt", "lv", "mt",
	"nl", "pl", "pt", "ro", "sk", "sl", "sv", "no", "is", "tr", "uk", "ar", "he"}

// wordsFile is generic_words.yaml as written.
type wordsFile struct {
	CookieWords  []string             `yaml:"cookie_words"`
	ConsentWords []string             `yaml:"consent_words"`
	Ignore       []string             `yaml:"ignore"`
	Policy       []string             `yaml:"policy"`
	NeverChoice  []string             `yaml:"never_choice"`
	Negation     []string             `yaml:"negation"`
	Languages    map[string]langWords `yaml:"languages"`
}

// langWords are one language's button phrases.
type langWords struct {
	Accept   []string `yaml:"accept"`
	Reject   []string `yaml:"reject"`
	Settings []string `yaml:"settings"`
	Save     []string `yaml:"save"`
}

// btnClass is what a button's text means.
type btnClass int

// The meanings a button text can have.
const (
	classNone btnClass = iota
	classAccept
	classReject
	classSettings
	classSave
)

func (c btnClass) String() string {
	return [...]string{"none", "accept", "reject", "settings", "save"}[c]
}

// phrase is one normalised phrase and its length in runes.
type phrase struct {
	text  string
	runes int
}

// lexicon is generic_words.yaml normalised, ready to match.
type lexicon struct {
	phrases [classSave + 1][]phrase
	never   map[string]bool
	negate  []string     // normalised negation words, matched as whole words
	scan    genericWords // what the page script gets
}

// genericWords are the word lists the generic script matches in the page.
type genericWords struct {
	Cookie  []string `json:"cookie_words"` // not "cookie": the scripts never touch a .cookie member
	Consent []string `json:"consent"`
	Ignore  []string `json:"ignore"`
	Policy  []string `json:"policy"`
}

// maxButtonRunes bounds a button text that can be a choice: a sentence
// that happens to hold "accept" is not a button label.
const maxButtonRunes = 80

// normText is how every text is compared: NFKC, lower case, combining
// marks dropped (so an accent or a missing one doesn't matter), format
// characters such as zero-width spaces and soft hyphens removed and any
// run of other characters that are not letters or digits read as one
// space. js/generic.js does the same in the page.
func normText(s string) string {
	s = strings.ToLower(norm.NFKC.String(s))
	var b strings.Builder
	space := true
	for _, r := range norm.NFD.String(s) {
		switch {
		case unicode.Is(unicode.M, r), unicode.Is(unicode.Cf, r):
			continue
		case r == 'ς':
			r = 'σ'
		case !unicode.IsLetter(r) && !unicode.IsNumber(r):
			if !space {
				b.WriteByte(' ')
				space = true
			}
			continue
		}
		b.WriteRune(r)
		space = false
	}
	return strings.TrimSpace(norm.NFC.String(b.String()))
}

// loadWords reads generic_words.yaml: every key known, every language
// with at least three Accept and three Reject phrases and no phrase
// meaning two things.
func loadWords(data []byte) (*lexicon, error) {
	var f wordsFile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("generic_words.yaml: %w", err)
	}
	lx := &lexicon{never: map[string]bool{}}
	normAll := func(list []string) []string {
		out := make([]string, 0, len(list))
		for _, w := range list {
			if n := normText(w); n != "" && !slices.Contains(out, n) {
				out = append(out, n)
			}
		}
		return out
	}
	lx.scan = genericWords{Cookie: normAll(f.CookieWords), Consent: normAll(f.ConsentWords), Ignore: normAll(f.Ignore), Policy: f.Policy}
	for _, w := range normAll(f.NeverChoice) {
		lx.never[w] = true
	}
	lx.negate = normAll(f.Negation)
	for _, code := range genericLanguages {
		if _, ok := f.Languages[code]; !ok {
			return nil, fmt.Errorf("generic_words.yaml: languages: %s missing", code)
		}
	}
	meaning := map[string]btnClass{}
	codes := make([]string, 0, len(f.Languages))
	for code := range f.Languages {
		codes = append(codes, code)
	}
	slices.Sort(codes)
	for _, code := range codes {
		if !slices.Contains(genericLanguages, code) {
			return nil, fmt.Errorf("generic_words.yaml: languages: %s isn't one of the 30", code)
		}
		lw := f.Languages[code]
		if len(lw.Accept) < 3 || len(lw.Reject) < 3 {
			return nil, fmt.Errorf("generic_words.yaml: languages: %s: want at least three accept and three reject phrases", code)
		}
		for c, list := range map[btnClass][]string{classAccept: lw.Accept, classReject: lw.Reject, classSettings: lw.Settings, classSave: lw.Save} {
			for _, p := range normAll(list) {
				if prev, ok := meaning[p]; ok && prev != c {
					return nil, fmt.Errorf("generic_words.yaml: languages: %s: %q is both %s and %s", code, p, prev, c)
				}
				if c == classAccept && lx.negated(" "+p+" ") {
					return nil, fmt.Errorf("generic_words.yaml: languages: %s: accept phrase %q holds a negation word", code, p)
				}
				if _, ok := meaning[p]; !ok {
					meaning[p] = c
					lx.phrases[c] = append(lx.phrases[c], phrase{text: p, runes: utf8.RuneCountInString(p)})
				}
			}
		}
	}
	for c := range lx.phrases {
		slices.SortFunc(lx.phrases[c], func(a, b phrase) int { return strings.Compare(a.text, b.text) })
	}
	return lx, nil
}

// negated reports whether padded (a normalised text with a space at
// each end) holds a negation word as a whole word.
func (lx *lexicon) negated(padded string) bool {
	for _, w := range lx.negate {
		if strings.Contains(padded, " "+w+" ") {
			return true
		}
	}
	return false
}

// classify says what a button's text means and how long the phrase
// that decided it is (longer is more specific). The longest phrase
// found as whole words wins; a tie between two meanings, a text that is
// exactly a never_choice phrase (for Accept and Reject), an Accept with
// a negation word in it or a text too long for a label means nothing.
// The negation guard is the backstop for the negated forms the word
// lists lack: "Ne pas accepter" holds "accepter" and an Accept run
// must never click it.
func (lx *lexicon) classify(text string) (btnClass, int) {
	n := normText(text)
	if n == "" || utf8.RuneCountInString(n) > maxButtonRunes {
		return classNone, 0
	}
	padded := " " + n + " "
	best, bestLen, tie := classNone, 0, false
	for c := classAccept; c <= classSave; c++ {
		for _, p := range lx.phrases[c] {
			if p.runes < bestLen || !strings.Contains(padded, " "+p.text+" ") {
				continue
			}
			switch {
			case p.runes > bestLen:
				best, bestLen, tie = c, p.runes, false
			case c != best:
				tie = true
			}
		}
	}
	if tie {
		return classNone, 0
	}
	if lx.never[n] && (best == classAccept || best == classReject) {
		return classNone, 0
	}
	if best == classAccept && lx.negated(padded) {
		return classNone, 0
	}
	return best, bestLen
}

// words is the compiled-in lexicon. TestGenericWords loads the file, so
// a broken one never ships.
var words = func() *lexicon {
	lx, err := loadWords(genericWordsYAML)
	if err != nil {
		panic(fmt.Sprintf("consent/cmp: %v", err))
	}
	return lx
}()

var genericJS = buildScript("generic")

// genButton is one control in a candidate container.
type genButton struct {
	Path     string      `json:"path"`
	Text     string      `json:"text"`
	Visible  bool        `json:"visible"`
	Kind     bool        `json:"kind"`
	Leaves   bool        `json:"leaves"`
	Policy   bool        `json:"policy"`
	Disabled bool        `json:"disabled"`
	Top      bool        `json:"top"`
	Box      consent.Box `json:"box"`
}

// clickable is the generic rule's test before a click: shown, a kind a
// visitor clicks, enabled, top-most and never a link that loads another
// page or a policy.
func (b genButton) clickable() bool {
	return b.Visible && b.Kind && !b.Disabled && b.Top && !b.Leaves && !b.Policy
}

// genContainer is one candidate banner.
type genContainer struct {
	Path    string `json:"path"`
	Frame   string `json:"frame"`
	Cookie  bool   `json:"cookie"`
	Consent bool   `json:"consent"`
	Ignored bool   `json:"ignored"`
	// Named is the id of the named rule whose detect selector or banner
	// is visible inside the container (or holds it), as the same scan
	// saw it; "" when none is.
	Named   string      `json:"named,omitempty"`
	Text    string      `json:"text"`
	Buttons []genButton `json:"buttons"`
}

type genResult struct {
	Containers []genContainer `json:"containers"`
}

// banner reports whether c reads as a cookie banner at all: a cookie
// noun, or a consent word without the words of a newsletter, login,
// cart, age gate or other pop-up.
func (c genContainer) banner() bool {
	return c.Cookie || (c.Consent && !c.Ignored)
}

// loc is a locator for a path inside c's document.
func (c genContainer) loc(path string) consent.Locator {
	return consent.Locator{CSS: path, Frame: c.Frame}
}

// best is c's clickable button meaning want with the most specific
// phrase; the first in the page on a tie.
func (c genContainer) best(want btnClass) (genButton, bool) {
	var got genButton
	bestLen := 0
	for _, b := range c.Buttons {
		if !b.clickable() {
			continue
		}
		if cl, n := words.classify(b.Text); cl == want && n > bestLen {
			got, bestLen = b, n
		}
	}
	return got, bestLen > 0
}

// choice reports whether c is a banner with an Accept or a Reject a
// visitor can click.
func (c genContainer) choice() bool {
	if !c.banner() {
		return false
	}
	_, a := c.best(classAccept)
	_, r := c.best(classReject)
	return a || r
}

// namedRule is a named rule's selectors as the generic script gets
// them: a container that holds one of them belongs to that rule.
type namedRule struct {
	ID   string            `json:"id"`
	Locs []consent.Locator `json:"locs"`
}

// namedFor is the generic script's named input for rules: every rule but
// the generic one, with its detect selectors and banner containers.
func namedFor(rules []*rule) []namedRule {
	var out []namedRule
	for _, r := range rules {
		if r.ID == genericID {
			continue
		}
		out = append(out, namedRule{ID: r.ID, Locs: slices.Concat(r.Selectors, r.Banners)})
	}
	return out
}

// genericScan runs the generic script in the page and in each
// cross-origin frame. named, when set, makes the script say which
// container holds a named rule's markup.
func genericScan(ctx context.Context, t consent.Tab, named ...namedRule) ([]genContainer, error) {
	in := struct {
		Words genericWords `json:"words"`
		Named []namedRule  `json:"named,omitempty"`
	}{words.scan, named}
	var top genResult
	raw, err := isolated(ctx, t, genericJS, in)
	if err != nil {
		return nil, err
	}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &top); err != nil {
			return nil, err
		}
	}
	out := top.Containers
	frames, err := t.Frames(ctx)
	if err != nil {
		return out, nil
	}
	for i, f := range frames {
		if i >= 32 {
			break
		}
		if !f.CrossOrigin {
			continue
		}
		var res genResult
		raw, err := isolated(ctx, t, genericJS, in, f)
		if err != nil || len(raw) == 0 || json.Unmarshal(raw, &res) != nil {
			continue
		}
		for _, c := range res.Containers {
			c.Frame = f.URL
			out = append(out, c)
		}
	}
	return clampContainers(out), nil
}

// The most of a generic scan Go keeps, whatever the script answered:
// the script stops at 20 containers of 50 controls and builds paths of
// at most maxPath bytes.
const (
	maxContainers = 40
	maxButtons    = 50
	maxPath       = 1024
)

// cutRunes keeps the first n runes of s. Unlike consent.SafeText it
// escapes nothing: the text is still to be classified; it is made safe
// to print where it is printed.
func cutRunes(s string, n int) string {
	for i := range s {
		if n == 0 {
			return s[:i]
		}
		n--
	}
	return s
}

// clampContainers keeps at most maxContainers containers of at most
// maxButtons controls each, with their texts cut to 200 runes. A container or control
// without a usable path is dropped: a path cut short would name another
// element.
func clampContainers(list []genContainer) []genContainer {
	out := make([]genContainer, 0, min(len(list), maxContainers))
	for _, c := range list {
		if len(out) == maxContainers {
			break
		}
		if c.Path == "" || len(c.Path) > maxPath {
			continue
		}
		c.Text = cutRunes(c.Text, 200)
		c.Named = cutRunes(c.Named, 64)
		btns := make([]genButton, 0, min(len(c.Buttons), maxButtons))
		for _, b := range c.Buttons {
			if len(btns) == maxButtons {
				break
			}
			if b.Path == "" || len(b.Path) > maxPath {
				continue
			}
			b.Text = cutRunes(b.Text, 200)
			btns = append(btns, b)
		}
		c.Buttons = btns
		out = append(out, c)
	}
	return out
}

// genericBanner is the first container that is a banner with a choice;
// ok false when there is none.
func genericBanner(ctx context.Context, t consent.Tab, named ...namedRule) (genContainer, bool, error) {
	list, err := genericScan(ctx, t, named...)
	if err != nil {
		return genContainer{}, false, err
	}
	for _, c := range list {
		if c.choice() {
			return c, true, nil
		}
	}
	return genContainer{}, false, nil
}

// genericShowing is the generic rule's BannerShowing: a banner with a
// choice is on screen.
func genericShowing(ctx context.Context, t consent.Tab) (bool, error) {
	_, ok, err := genericBanner(ctx, t)
	return ok, err
}

// genericTarget is the target a generic Accept or Reject starts with,
// from one scan: the best button meaning k in the banner, else for a
// Reject (when second is set) the button that opens its settings.
func genericTarget(list []genContainer, k consent.RunKind, second bool) *target {
	want := classAccept
	if k == consent.RunReject {
		want = classReject
	}
	for _, c := range list {
		if !c.choice() {
			continue
		}
		in := []consent.Locator{c.loc(c.Path)}
		if b, ok := c.best(want); ok {
			return genericButtonTarget(c, b, 1, in)
		}
		if want == classReject && second {
			if b, ok := c.best(classSettings); ok {
				return genericButtonTarget(c, b, 2, in)
			}
		}
	}
	return nil
}

func genericButtonTarget(c genContainer, b genButton, layer int, in []consent.Locator) *target {
	l := c.loc(b.Path)
	return &target{
		layer:  layer,
		chain:  one(l),
		within: in,
		probe:  probeResult{Found: true, Visible: true, Kind: true, Inside: true, Top: true, Text: b.Text, Box: b.Box},
	}
}

// genericSave finds, after the settings opened, the button that saves
// them (or rejects all) in a cookie banner or dialog, skipping the
// opener; container is where its toggles are.
func genericSave(list []genContainer, opener string) (save *chain, container genContainer, ok bool) {
	for _, want := range []btnClass{classSave, classReject} {
		for _, c := range list {
			if !c.banner() {
				continue
			}
			for _, b := range c.Buttons {
				if b.Path == opener || !b.clickable() {
					continue
				}
				if cl, _ := words.classify(b.Text); cl == want {
					ch := one(c.loc(b.Path))
					return &ch, c, true
				}
			}
		}
	}
	return nil, genContainer{}, false
}

// genericToggles are the switches of a settings dialog: checkboxes and
// role=switch elements inside it, switched off when on and enabled.
func genericToggles(c genContainer) []consent.Locator {
	css := c.Path + " input[type=checkbox]:not([disabled]), " + c.Path + " [role=switch]:not([aria-disabled=true])"
	return []consent.Locator{{CSS: css, Frame: c.Frame}}
}

// genericLocate waits up to --banner-wait for the generic banner's
// Accept or Reject; for a Reject, after secondGrace, the button that
// opens its settings.
func (d *driver) genericLocate(ctx context.Context, t consent.Tab, p *consent.Plan, k consent.RunKind) (*target, consent.Reason, error) {
	start := t.Now()
	deadline := start + p.BannerWait.Milliseconds()
	for {
		list, err := genericScan(ctx, t)
		if err != nil && ctx.Err() != nil {
			return nil, consent.ReasonNone, ctx.Err()
		}
		now := t.Now()
		if err == nil {
			second := now-start >= secondGrace.Milliseconds() || now >= deadline
			if tg := genericTarget(list, k, second); tg != nil {
				return tg, consent.ReasonNone, nil
			}
		}
		if now >= deadline {
			return nil, consent.ReasonNoButton, nil
		}
		if err := d.sleep(ctx, pollEvery); err != nil {
			return nil, consent.ReasonNone, err
		}
	}
}

// genericSettings finishes a generic Reject in the settings the opener
// showed: it waits for a Save (or a Reject) there, switches the
// dialog's switches off and saves.
func (d *driver) genericSettings(ctx context.Context, t consent.Tab, p *consent.Plan, r *rule, k consent.RunKind, tg *target, ch *consent.Choice, before *consent.CMPState) (*consent.Choice, consent.Reason, error) {
	deadline := t.Now() + p.BannerWait.Milliseconds()
	for {
		list, err := genericScan(ctx, t)
		if err != nil && ctx.Err() != nil {
			return ch, consent.ReasonNone, ctx.Err()
		}
		if err == nil {
			if save, c, ok := genericSave(list, tg.chain.Steps[0].CSS); ok {
				if err := switchOff(ctx, t, genericToggles(c), false); err != nil {
					return ch, consent.ReasonNone, err
				}
				ch.Selector = consent.SafeText(tg.chain.Raw+" >> "+save.Raw, selectorWidth)
				return d.clickAndVerify(ctx, t, p, r, k, *save, nil, ch, before)
			}
		}
		if t.Now() >= deadline {
			return ch, consent.ReasonNoButton, nil
		}
		if err := d.sleep(ctx, pollEvery); err != nil {
			return ch, consent.ReasonNone, err
		}
	}
}

// staysGone reloads the page and watches it for --settle: the generic
// banner must not come back. A banner that only hid comes back on the
// next load, since nothing stored the choice.
func (d *driver) staysGone(ctx context.Context, t consent.Tab, p *consent.Plan) (bool, error) {
	if err := t.Reload(ctx); err != nil {
		return false, err
	}
	deadline := t.Now() + p.Settle.Milliseconds()
	for {
		showing, err := genericShowing(ctx, t)
		if err != nil && ctx.Err() != nil {
			return false, ctx.Err()
		}
		if err == nil && showing {
			return false, nil
		}
		if t.Now() >= deadline {
			return true, nil
		}
		if err := d.sleep(ctx, pollEvery); err != nil {
			return false, err
		}
	}
}
