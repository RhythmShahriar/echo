// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package compare

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strings"
	"sync"

	"github.com/RhythmShahriar/echo/model"
)

// DiffLine is one line of a line diff. Op is "add" (only in the after
// text), "del" (only in the before text) or "ctx" (in both, shown around
// a change). Line is the line's number in the after text, or in the
// before text for "del".
type DiffLine struct {
	Op   string `json:"op"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

// Hunk is a run of changed lines with up to Context unchanged lines on
// each side.
type Hunk []DiffLine

// Context is how many unchanged lines a hunk shows around its change.
const Context = 2

// Diff ops.
const (
	OpAdd = "add"
	OpDel = "del"
	OpCtx = "ctx"
)

// maxEdits bounds the diff's work: past this many differing lines the
// texts are shown as replaced whole, which is what they are by then.
const maxEdits = 500

// Diff compares two texts line by line, as the user would read them:
// blank lines, spacing and what changes on every run (see Normalize)
// don't count. It returns nil when nothing else differs.
func Diff(before, after string) []Hunk {
	a, b := diffLines(before), diffLines(after)
	ops := script(a, b)
	return hunks(ops, a, b)
}

// ChangeDiff returns the hunks of a text or payload change, worked out
// from its full Before and After. Any other change has none. The hunks
// are shared between calls, so callers must not change them.
func ChangeDiff(c model.Change) []Hunk {
	if !diffable(c.Check) {
		return nil
	}
	return diffs.get(c.Before, c.After)
}

// diffCache keeps the hunks of recent texts. Grouping, Key and every
// report format ask for the same change's diff, and normalizing each line
// plus Myers on two 256 KiB texts is worth doing once. A model.Change has
// no field to keep them in yet, so they are cached by the texts' hash.
type diffCache struct {
	mu sync.Mutex
	by map[[sha256.Size]byte][]Hunk
}

// maxCachedDiffs bounds the cache; a full cache starts over, which keeps
// it simple and costs only a recompute.
const maxCachedDiffs = 256

var diffs diffCache

func (d *diffCache) get(before, after string) []Hunk {
	h := sha256.New()
	fmt.Fprintf(h, "%d:", len(before))
	h.Write([]byte(before))
	h.Write([]byte(after))
	var k [sha256.Size]byte
	h.Sum(k[:0])
	d.mu.Lock()
	hs, ok := d.by[k]
	d.mu.Unlock()
	if ok {
		return hs
	}
	hs = Diff(before, after)
	d.mu.Lock()
	if d.by == nil || len(d.by) >= maxCachedDiffs {
		d.by = map[[sha256.Size]byte][]Hunk{}
	}
	d.by[k] = hs
	d.mu.Unlock()
	return hs
}

func diffable(check string) bool { return check == "message/text" || check == "message/payload" }

// Summary says what a hunk changed in one line: "line 9 added: Order
// total: $42.00", "line 4 changed: Hi → Hello", "lines 3-5 removed: …".
func (h Hunk) Summary() string {
	var adds, dels []DiffLine
	for _, l := range h {
		switch l.Op {
		case OpAdd:
			adds = append(adds, l)
		case OpDel:
			dels = append(dels, l)
		}
	}
	where := func(ls []DiffLine) string {
		if len(ls) == 1 {
			return fmt.Sprintf("line %d", ls[0].Line)
		}
		return fmt.Sprintf("lines %d-%d", ls[0].Line, ls[len(ls)-1].Line)
	}
	switch {
	case len(dels) == 0 && len(adds) > 0:
		return where(adds) + " added: " + short(adds[0].Text)
	case len(adds) == 0 && len(dels) > 0:
		return where(dels) + " removed: " + short(dels[0].Text)
	case len(adds) == 1 && len(dels) == 1:
		return where(adds) + " changed: " + short(dels[0].Text) + " → " + short(adds[0].Text)
	case len(adds) > 0:
		return where(adds) + " changed: " + short(adds[0].Text)
	}
	return ""
}

func short(s string) string {
	s = strings.TrimSpace(space.ReplaceAllString(s, " "))
	if r := []rune(s); len(r) > 80 {
		return string(r[:79]) + "…"
	}
	return s
}

// line is a non-blank line of a text: its number, its text as written and
// the key the diff compares.
type line struct {
	n         int
	text, key string
}

func diffLines(s string) []line {
	var out []line
	for i, t := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		t = strings.TrimRight(t, " \t\r")
		if k := lineKey(t); k != "" {
			out = append(out, line{n: i + 1, text: t, key: k})
		}
	}
	return out
}

var (
	uuidRe  = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	isoRe   = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}(?:[T ]\d{2}:\d{2}(?::\d{2}(?:\.\d+)?)?(?:Z|[+-]\d{2}:?\d{2})?)?\b`)
	monthRe = `(?:jan(?:uary)?|feb(?:ruary)?|mar(?:ch)?|apr(?:il)?|may|june?|july?|aug(?:ust)?|sep(?:t(?:ember)?)?|oct(?:ober)?|nov(?:ember)?|dec(?:ember)?)\.?`
	dayRe   = `(?:(?:mon|tue|wed|thu|fri|sat|sun)[a-z]*\.?,?\s+)?`
	// "Sat, 24 Oct 2026" (RFC 2822), "24 October 2026", "October 24, 2026".
	longDateRe = regexp.MustCompile(`(?i)\b` + dayRe + `(?:\d{1,2}(?:st|nd|rd|th)?\s+` + monthRe + `,?\s+\d{4}|` + monthRe + `\s+\d{1,2}(?:st|nd|rd|th)?,?\s+\d{4})\b`)
	// "10/24/2026", "24.10.2026", "2026/10/24".
	numDateRe = regexp.MustCompile(`\b(?:\d{1,2}[/.-]\d{1,2}[/.-]\d{2,4}|\d{4}/\d{1,2}/\d{1,2})\b`)
	// "09:00", "9:00 pm", "09:00:00 +0000", "14:30 CEST".
	timeRe = regexp.MustCompile(`(?i)\b\d{1,2}:\d{2}(?::\d{2})?(?:\s?[ap]\.?m\b\.?)?(?:\s?(?:[+-]\d{4}|utc|gmt|[a-z]{1,3}[ds]t)\b)?`)
	// "order W-7462", "Invoice #1042", "ref: AB-123": the word stays, the
	// number goes. The number must hold a digit, so "order total" stays.
	orderRe = regexp.MustCompile(`(?i)\b(order|invoice|receipt|ref(?:erence)?|booking|confirmation|ticket)(\s*(?:number|no\.?|num|id)?\s*[:#]?\s*)([a-z]{0,4}[-#]?\d[\w-]*)`)
	// "coupon SAVE-X7K2", "code: WELCOME10": an upper-case code after the word.
	couponRe = regexp.MustCompile(`((?i:\b(?:coupon|promo|voucher|discount)(?:\s+code)?|\bcode))(\s*[:=]?\s*)([A-Z0-9][A-Z0-9_-]{3,})\b`)
	tokenRe  = regexp.MustCompile(`\b[A-Za-z0-9_-]{24,}\b|\b[0-9a-fA-F]{16,}\b`)
)

// Normalize is a line with what changes on every run replaced by a
// placeholder: dates ({date}: ISO, RFC 2822, "24 October 2026",
// "10/24/2026"), times ({time}), order, invoice and reference numbers
// ({number}), coupon codes ({code}), UUIDs and long tokens ({uuid},
// {token}) and Soundcheck's or Echo's test data. Spacing is collapsed.
func Normalize(s string) string {
	s = uuidRe.ReplaceAllString(s, "{uuid}")
	s = isoRe.ReplaceAllString(s, "{date}")
	s = longDateRe.ReplaceAllString(s, "{date}")
	s = numDateRe.ReplaceAllString(s, "{date}")
	s = timeRe.ReplaceAllString(s, "{time}")
	s = orderRe.ReplaceAllString(s, "${1}${2}{number}")
	s = couponRe.ReplaceAllString(s, "${1}${2}{code}")
	s = tokenRe.ReplaceAllStringFunc(s, func(t string) string {
		if strings.ContainsAny(t, "0123456789") {
			return "{token}"
		}
		return t
	})
	return strings.TrimSpace(display(s))
}

// lineKey is what the diff compares: Normalize, then without case and
// with numbers as "#", as the old whole-text fingerprint did. Only a
// change in words counts, so a price or count that differs run to run
// isn't noise.
func lineKey(s string) string {
	return strings.TrimSpace(template(volatileRe.ReplaceAllString(Normalize(s), "~")))
}

// script is the shortest edit script from a to b, one byte per step:
// '=' (keep a line), '-' (drop a line of a), '+' (add a line of b).
func script(a, b []line) []byte {
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre].key == b[pre].key {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf].key == b[len(b)-1-suf].key {
		suf++
	}
	out := bytes.Repeat([]byte{'='}, pre)
	out = append(out, myers(a[pre:len(a)-suf], b[pre:len(b)-suf])...)
	return append(out, bytes.Repeat([]byte{'='}, suf)...)
}

// myers is Myers' O(ND) diff ("An O(ND) Difference Algorithm and Its
// Variations", 1986), keeping each round's furthest points to walk the
// path back. Past maxEdits it gives up and replaces a with b.
func myers(a, b []line) []byte {
	n, m := len(a), len(b)
	if n == 0 || m == 0 {
		return append(bytes.Repeat([]byte{'-'}, n), bytes.Repeat([]byte{'+'}, m)...)
	}
	limit := min(n+m, maxEdits)
	off := limit + 1
	v := make([]int, 2*limit+3)
	var trace [][]int
	for d := 0; d <= limit; d++ {
		trace = append(trace, append([]int(nil), v...))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1] // down: insert
			} else {
				x = v[off+k-1] + 1 // right: delete
			}
			y := x - k
			for x < n && y < m && a[x].key == b[y].key {
				x++
				y++
			}
			v[off+k] = x
			if x >= n && y >= m {
				return backtrack(trace, off, n, m)
			}
		}
	}
	return append(bytes.Repeat([]byte{'-'}, n), bytes.Repeat([]byte{'+'}, m)...)
}

func backtrack(trace [][]int, off, n, m int) []byte {
	var rev []byte
	x, y := n, m
	for d := len(trace) - 1; d >= 0; d-- {
		v := trace[d]
		k := x - y
		var pk int
		if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px := v[off+pk]
		py := px - pk
		for x > px && y > py {
			rev = append(rev, '=')
			x--
			y--
		}
		if d > 0 {
			if x == px {
				rev = append(rev, '+')
				y--
			} else {
				rev = append(rev, '-')
				x--
			}
		}
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

// hunks groups an edit script into hunks with Context lines around each
// change; changes closer than twice that share a hunk.
func hunks(ops []byte, a, b []line) []Hunk {
	type step struct {
		op   byte
		l    line
		near bool
	}
	steps := make([]step, len(ops))
	i, j := 0, 0
	for s, op := range ops {
		switch op {
		case '=':
			steps[s] = step{op: op, l: b[j]}
			i++
			j++
		case '-':
			steps[s] = step{op: op, l: a[i]}
			i++
		case '+':
			steps[s] = step{op: op, l: b[j]}
			j++
		}
	}
	for s := range steps {
		if steps[s].op == '=' {
			continue
		}
		for c := max(0, s-Context); c <= min(len(steps)-1, s+Context); c++ {
			steps[c].near = true
		}
	}
	var out []Hunk
	var cur Hunk
	for _, st := range steps {
		if !st.near {
			if cur != nil {
				out = append(out, cur)
				cur = nil
			}
			continue
		}
		op := OpCtx
		switch st.op {
		case '-':
			op = OpDel
		case '+':
			op = OpAdd
		}
		cur = append(cur, DiffLine{Op: op, Line: st.l.n, Text: st.l.text})
	}
	if cur != nil {
		out = append(out, cur)
	}
	return out
}

// signature is a diff's changed lines by key, so the same edit made in
// several actions (with different test data around it) groups as one.
func signature(hs []Hunk) string {
	var b strings.Builder
	for _, h := range hs {
		for _, l := range h {
			if l.Op != OpCtx {
				b.WriteString(l.Op)
				b.WriteByte(' ')
				b.WriteString(lineKey(l.Text))
				b.WriteByte('\n')
			}
		}
	}
	return b.String()
}

var (
	invisibleRe = regexp.MustCompile(`(?is)<!--.*?-->|<(head|style|script|title)\b.*?</(head|style|script|title)\s*>`)
	breakRe     = regexp.MustCompile(`(?i)<br\s*/?>|</?(p|div|tr|li|h[1-6]|table|tbody|thead|ul|ol|blockquote|section|article|header|footer)\b[^>]*>`)
	cellRe      = regexp.MustCompile(`(?i)</t[dh]\s*>`)
)

// visibleText is the text a reader sees in an HTML email, one block per
// line: no markup, styles, scripts or comments, entities decoded.
func visibleText(s string) string {
	s = invisibleRe.ReplaceAllString(s, "")
	s = breakRe.ReplaceAllString(s, "\n")
	s = cellRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(tags.ReplaceAllString(s, ""))
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(space.ReplaceAllString(strings.ReplaceAll(l, "\u00a0", " "), " ")); l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// prettyJSON indents a JSON body with sorted keys, one value per line, so
// a line diff points at the value that changed. ok is false for a body
// that isn't JSON.
func prettyJSON(body string) (string, bool) {
	dec := json.NewDecoder(strings.NewReader(body))
	dec.UseNumber() // keep big IDs and amounts exactly as sent
	var v any
	if dec.Decode(&v) != nil || dec.More() {
		return "", false
	}
	switch v.(type) {
	case map[string]any, []any:
	default:
		return "", false
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if enc.Encode(v) != nil {
		return "", false
	}
	return strings.TrimRight(b.String(), "\n"), true
}
