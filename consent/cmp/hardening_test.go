// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cmp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
)

// jsTok is one token of a script: an identifier, a punctuator, a
// number, or a string or regular expression literal (whose contents
// are data, never checked as code).
type jsTok struct {
	kind byte // i ident, p punct, n number, s string, r regex
	text string
}

// jsPuncts are the punctuators the tokenizer knows, longest first so
// "===" is never read as "==" and "=".
var jsPuncts = []string{
	">>>=", "...", "===", "!==", "**=", "<<=", ">>=", ">>>", "&&=", "||=", "??=",
	"=>", "==", "!=", "<=", ">=", "&&", "||", "??", "?.", "++", "--", "+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=", "**", "<<", ">>",
}

// regexAfter are the keywords a regular expression literal may follow
// (after an identifier that isn't one, a slash divides).
var regexAfter = []string{"return", "typeof", "case", "do", "else", "in", "of", "new", "delete", "void", "throw"}

// tokenizeJS splits src into tokens, dropping whitespace and comments.
// It is small on purpose: enough for the scripts Echo ships, which use
// no template literals (they are refused, so code can't hide in one).
func tokenizeJS(src string) ([]jsTok, error) {
	var out []jsTok
	isID := func(c byte) bool {
		return c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 0x80
	}
	regexOK := func() bool {
		if len(out) == 0 {
			return true
		}
		last := out[len(out)-1]
		switch last.kind {
		case 'p':
			return last.text != ")" && last.text != "]" && last.text != "}"
		case 'i':
			return slices.Contains(regexAfter, last.text)
		}
		return false
	}
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case strings.HasPrefix(src[i:], "//"):
			end := strings.IndexByte(src[i:], '\n')
			if end < 0 {
				end = len(src) - i
			}
			i += end
		case strings.HasPrefix(src[i:], "/*"):
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return nil, fmt.Errorf("offset %d: unclosed comment", i)
			}
			i += end + 4
		case c == '`':
			return nil, fmt.Errorf("offset %d: template literal", i)
		case c == '"' || c == '\'':
			j := i + 1
			for ; j < len(src) && src[j] != c; j++ {
				switch src[j] {
				case '\\':
					j++
				case '\n':
					return nil, fmt.Errorf("offset %d: unclosed string", i)
				}
			}
			if j >= len(src) {
				return nil, fmt.Errorf("offset %d: unclosed string", i)
			}
			out = append(out, jsTok{'s', src[i+1 : j]})
			i = j + 1
		case c == '/' && regexOK():
			j, class := i+1, false
		scan:
			for ; j < len(src); j++ {
				switch src[j] {
				case '\\':
					j++
				case '[':
					class = true
				case ']':
					class = false
				case '\n':
					return nil, fmt.Errorf("offset %d: unclosed regular expression", i)
				case '/':
					if !class {
						break scan
					}
				}
			}
			if j >= len(src) {
				return nil, fmt.Errorf("offset %d: unclosed regular expression", i)
			}
			j++
			for j < len(src) && isID(src[j]) {
				j++
			}
			out = append(out, jsTok{'r', src[i:j]})
			i = j
		case c >= '0' && c <= '9':
			j := i
			for j < len(src) && (isID(src[j]) || src[j] == '.') {
				j++
			}
			out = append(out, jsTok{'n', src[i:j]})
			i = j
		case isID(c):
			j := i
			for j < len(src) && isID(src[j]) {
				j++
			}
			out = append(out, jsTok{'i', src[i:j]})
			i = j
		default:
			p := string(c)
			for _, q := range jsPuncts {
				if strings.HasPrefix(src[i:], q) {
					p = q
					break
				}
			}
			out = append(out, jsTok{'p', p})
			i += len(p)
		}
	}
	return out, nil
}

// deniedNames are identifiers a read-only script never needs: network
// calls, storage and cookie access, code from strings, DOM writes and
// synthetic events. Comments and strings may name them.
var deniedNames = []string{
	"fetch", "XMLHttpRequest", "sendBeacon", "WebSocket", "EventSource", "Worker", "SharedWorker", "importScripts", "import",
	"eval", "Function", "setTimeout", "setInterval", "postMessage", "open",
	"cookieStore", "localStorage", "sessionStorage", "indexedDB", "caches",
	"innerHTML", "outerHTML", "insertAdjacentHTML", "insertAdjacentElement", "insertAdjacentText", "write", "writeln", "execCommand",
	"appendChild", "removeChild", "replaceChild", "insertBefore", "append", "prepend", "before", "after", "replaceWith", "replaceChildren",
	"remove", "setAttribute", "removeAttribute", "toggleAttribute", "attachShadow", "classList", "style", "dataset",
	"click", "dispatchEvent", "submit", "requestSubmit", "scrollIntoView", "assign", "reload", "pushState", "replaceState",
}

// deniedMembers are property names a script never reads or calls,
// though a local may share the name (generic.js keeps a cookie flag).
var deniedMembers = []string{"cookie"}

// assignOps are the operators that write to what stands left of them.
var assignOps = []string{"=", "+=", "-=", "*=", "/=", "%=", "**=", "<<=", ">>=", ">>>=", "&=", "|=", "^=", "&&=", "||=", "??=", "++", "--"}

// jsProblems lists what makes src more than a reader of the page: a
// denied name, a write to a property (a.b = c; a[k] = c is allowed for
// the script's own result objects, which the checks below keep to
// names the script declared) or a template literal.
func jsProblems(src string) []string {
	toks, err := tokenizeJS(src)
	if err != nil {
		return []string{err.Error()}
	}
	var out []string
	locals := map[string]bool{}
	for i, t := range toks {
		if t.kind != 'i' && t.kind != 'p' {
			continue
		}
		prev := jsTok{}
		if i > 0 {
			prev = toks[i-1]
		}
		if t.kind == 'i' {
			if prev.kind == 'i' && (prev.text == "const" || prev.text == "let" || prev.text == "var") {
				locals[t.text] = true
			}
			if slices.Contains(deniedNames, t.text) || (prev.text == "." && slices.Contains(deniedMembers, t.text)) {
				out = append(out, fmt.Sprintf("uses %s", t.text))
			}
			continue
		}
		if !slices.Contains(assignOps, t.text) {
			continue
		}
		switch {
		case i >= 2 && prev.kind == 'i' && toks[i-2].text == ".":
			out = append(out, fmt.Sprintf("writes the property %s", prev.text))
		case prev.text == "]":
			// a[k] = v: allowed only on an object the script made.
			depth, j := 0, i-1
			for ; j >= 0; j-- {
				if toks[j].text == "]" {
					depth++
				} else if toks[j].text == "[" {
					depth--
					if depth == 0 {
						break
					}
				}
			}
			if j < 1 || toks[j-1].kind != 'i' || !locals[toks[j-1].text] || (j >= 2 && toks[j-2].text == ".") {
				out = append(out, "writes an element of an object the script didn't make")
			}
		case (t.text == "++" || t.text == "--") && i+3 < len(toks) && toks[i+2].text == ".":
			out = append(out, fmt.Sprintf("writes the property %s", toks[i+3].text))
		}
	}
	return out
}

// jsSources are every script the driver can run in a page: each
// embedded file, each built isolated-world script and the main-world
// globals check.
func jsSources(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	files, err := fs.Glob(jsFS, "js/*.js")
	if err != nil || len(files) == 0 {
		t.Fatalf("no embedded scripts: %v", err)
	}
	for _, f := range files {
		b, _ := jsFS.ReadFile(f)
		out[f] = string(b)
	}
	for name, js := range map[string]string{"scan": scanJS, "probe": probeJS, "toggles": togglesJS, "generic": genericJS} {
		out["built "+name] = js
	}
	d, err := load(rulesFS, "rules")
	if err != nil {
		t.Fatal(err)
	}
	out["globals"] = d.globalsJS
	return out
}

// TestJSIsReadOnly reads every script the driver runs as data: no
// network call, no cookie or storage access, no DOM change (focus is
// the one thing a script may do; none needs it yet) and no template
// literal to hide code in.
func TestJSIsReadOnly(t *testing.T) {
	for name, src := range jsSources(t) {
		if p := jsProblems(src); len(p) > 0 {
			t.Errorf("%s: %s", name, strings.Join(p, "; "))
		}
	}
}

// TestJSCheckCatches proves the check: each line must be caught; the
// same words inside strings, comments and regular expressions must
// not be.
func TestJSCheckCatches(t *testing.T) {
	for _, src := range []string{
		`fetch("/x")`,
		`const x = new XMLHttpRequest()`,
		`navigator.sendBeacon(u, d)`,
		`document.cookie = "a=1"`,
		`document . cookie="a=1"`,
		`const c = document.cookie`,
		`localStorage.setItem("k", "v")`,
		`sessionStorage.getItem("k")`,
		`el.remove()`,
		`el.innerHTML = "<b>x</b>"`,
		`el.style.display = "none"`,
		`el.classList.add("hide")`,
		`el.setAttribute("hidden", "")`,
		`el.click()`,
		`el.dispatchEvent(new Event("click"))`,
		`document.body.appendChild(x)`,
		`el.checked = false`,
		`el.value += "x"`,
		`el.hidden = true`,
		`el.dataset.x = "1"`,
		`location.href = "https://example.test/"`,
		`window.open("https://example.test/")`,
		`eval("1")`,
		`new Function("return 1")`,
		"const s = `${x}`",
		`el["hidden"] = true`,
		`document.body[k] = 1`,
		`x.count++`,
		`++x.count`,
		`import("./x.js")`,
		`setTimeout(f, 1)`,
	} {
		if len(jsProblems(src)) == 0 {
			t.Errorf("not caught: %s", src)
		}
	}
	for _, src := range []string{
		`// fetch, document.cookie = 1, el.remove()` + "\nconst a = 1;",
		`/* innerHTML = "x"; style.display */ const b = 2;`,
		`const s = "fetch(document.cookie) el.remove() style.x";`,
		`const r = /^javascript:/i.test(raw);`,
		`const q = String(raw).replace(/fetch\(/g, "");`,
		`const out = {}; out[k] = 1; out.list.push(1);`,
		`if (a == b || a === c || a != d || a !== e || a <= f || a >= g) {}`,
		`for (let i = 0; i < 3; i++) {}`,
		`const v = a / b / c; const w = (a) / 2;`,
		`el.focus();`,
		`const cs = win.getComputedStyle(el); const t = el.innerText;`,
	} {
		if p := jsProblems(src); len(p) > 0 {
			t.Errorf("false alarm on %s: %v", src, p)
		}
	}
	// A local's own fields are still properties: out.list = [] counts,
	// so scripts build their result in one literal.
	if p := jsProblems(`const out = {}; out.list = [];`); len(p) == 0 {
		t.Error("a property write on a local isn't caught")
	}
	if p := jsProblems(`const out = {}; out[k] = 1;`); len(p) != 0 {
		t.Errorf("an element write on a local is caught: %v", p)
	}
	for _, bad := range []string{`"abc`, `/* x`, `const r = /abc`, "`x`"} {
		if _, err := tokenizeJS(bad); err == nil {
			t.Errorf("tokenizeJS(%q) gave no error", bad)
		}
	}
}

// TestJSCaps checks the scripts bound what they return: lists cut at
// MAX (200) entries, texts at 200 characters and paths dropped over
// PATH_MAX, in the source itself.
func TestJSCaps(t *testing.T) {
	lib, _ := jsFS.ReadFile("js/lib.js")
	for _, want := range []string{"const MAX = 200;", ".slice(0, MAX)", "const PATH_MAX = 1024;", `return path.length > PATH_MAX ? "" : path;`} {
		if !bytes.Contains(lib, []byte(want)) {
			t.Errorf("lib.js lacks %q", want)
		}
	}
	for _, f := range []string{"scan", "probe", "toggles"} {
		b, _ := jsFS.ReadFile("js/" + f + ".js")
		if !bytes.Contains(b, []byte(".slice(0, MAX)")) {
			t.Errorf("%s.js: no list capped at MAX", f)
		}
	}
	gen, _ := jsFS.ReadFile("js/generic.js")
	for _, want := range []string{"found.length < 20", ".slice(0, 50)", "src.slice(0, MAX)", "out.slice(0, 20)"} {
		if !bytes.Contains(gen, []byte(want)) {
			t.Errorf("generic.js lacks %q", want)
		}
	}
}

// TestAnswerCap: an answer over 256 KiB is an error, never decoded.
func TestAnswerCap(t *testing.T) {
	pg := newFakePage()
	big := `{"x":"` + strings.Repeat("a", maxAnswer) + `"}`
	pg.tab.Isolated["cmp-probe"] = consenttestAnswer(big)
	if _, err := probe(t.Context(), pg.tab, []consent.Locator{{CSS: "#a"}}, nil); err == nil || !strings.Contains(err.Error(), "the cap is 262144") {
		t.Fatalf("probe err = %v", err)
	}
	pg.tab.Isolated["cmp-scan"] = consenttestAnswer(big)
	if _, err := scanFrames(t.Context(), pg.tab, scanInput{}); err == nil {
		t.Fatal("scan took an oversized answer")
	}
	pg.tab.Isolated["cmp-generic"] = consenttestAnswer(big)
	if _, err := genericScan(t.Context(), pg.tab); err == nil {
		t.Fatal("generic took an oversized answer")
	}
	small := `{"#a":{"found":true}}`
	pg.tab.Isolated["cmp-probe"] = consenttestAnswer(small)
	if res, err := probe(t.Context(), pg.tab, []consent.Locator{{CSS: "#a"}}, nil); err != nil || !res["#a"].Found {
		t.Fatalf("small answer: %v %v", res, err)
	}
}

func consenttestAnswer(s string) consenttest.Answer {
	return consenttest.Answer{Result: json.RawMessage(s)}
}

func TestClampContainers(t *testing.T) {
	long := strings.Repeat("é", 300)
	var list []genContainer
	for i := range 45 {
		c := cookieBar()
		c.Path = fmt.Sprintf("#c%d", i)
		c.Text = long
		for j := range 60 {
			c.Buttons = append(c.Buttons, genButton{Path: fmt.Sprintf("#b%d", j), Text: long})
		}
		list = append(list, c)
	}
	list[0].Path = ""
	list[1].Path = strings.Repeat("a", maxPath+1)
	list[2].Buttons[0].Path = ""
	got := clampContainers(list)
	if len(got) != maxContainers || got[0].Path != "#c2" {
		t.Fatalf("kept %d containers, first %q", len(got), got[0].Path)
	}
	if len(got[0].Buttons) != maxButtons || got[0].Buttons[0].Path != "#b1" {
		t.Fatalf("kept %d buttons, first %q", len(got[0].Buttons), got[0].Buttons[0].Path)
	}
	if n := len([]rune(got[0].Text)); n != 200 || len([]rune(got[0].Buttons[0].Text)) != 200 {
		t.Fatalf("text is %d runes", n)
	}
	if cutRunes("ab", 5) != "ab" || cutRunes("äbc", 1) != "ä" || cutRunes("abc", 0) != "" {
		t.Fatal("cutRunes")
	}
	if got := clampContainers(nil); len(got) != 0 {
		t.Fatalf("nil: %v", got)
	}
}

// listingGolden is what `consent cmps` shows, as data: the pack's
// version, count and hash and every rule's row. The cli lane renders
// it; a rule change shows here and in review.
type listingGolden struct {
	Info  consent.DataInfo
	Rules []consent.CMPRuleInfo
}

// TestRulesListingGolden compares Info and Rules of the shipped pack
// with testdata/cmps.golden.json (go test ./consent/cmp -run
// TestRulesListingGolden -update rewrites it).
func TestRulesListingGolden(t *testing.T) {
	d, err := load(rulesFS, "rules")
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.MarshalIndent(listingGolden{Info: d.Info(), Rules: d.Rules()}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", "cmps.golden.json")
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("Rules() or Info() changed; check the diff, then: go test ./consent/cmp -run TestRulesListingGolden -update\n%s", got)
	}
	if d.Info().Version != consent.DataVersion || d.Info().Count != len(d.Rules()) || len(d.Info().SHA256) != 64 {
		t.Errorf("Info = %+v", d.Info())
	}
	if consent.Linked().CMP.Info() != d.Info() {
		t.Error("the linked driver's Info isn't the shipped pack's")
	}
}
