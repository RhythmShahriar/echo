// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package compare diffs two runs: for each action that's in both, it
// matches the messages sent before and after and reports the ones that
// stopped (BROKE), appeared (NEW), changed (who they go to, the subject,
// the text, the fields of a payload, how many times) or problems that went
// away (FIXED). What an action new since the snapshot sends is NEW. The
// same change across many actions is one line.
package compare

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/check"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/recognize"
)

// Compare diffs before and after, as the zero Options do.
func Compare(before, after *model.Run, beforeDir, afterDir, version string) *model.Comparison {
	return compareRuns(before, after, beforeDir, afterDir, version, Options{})
}

func compareRuns(before, after *model.Run, beforeDir, afterDir, version string, o Options) *model.Comparison {
	out := &model.Comparison{Schema: model.Schema, Tool: "rhythm-echo", Version: version, Created: time.Now().UTC(),
		Before: model.RunRef{Target: before.Target, Name: before.Name, Dir: beforeDir, Started: before.Started},
		After:  model.RunRef{Target: after.Target, Name: after.Name, Dir: afterDir, Started: after.Started}}
	g := grouper{tune: o.tune}
	var notes []string
	for _, a := range after.Actions {
		b := before.Action(a.ID)
		if b != nil {
			out.Actions++
		}
		if a.Error != "" {
			// Nothing it sent can be trusted as the whole story, so it isn't
			// compared. Failing in the snapshot too doesn't make it "the
			// same": a site that's down at both ends was never checked.
			title := "Action fails"
			if b != nil && b.Error != "" {
				title = "Action still fails"
			}
			fix := "The request that triggers the messages failed, so nothing it sends was compared: fix that first."
			silent := b != nil && b.Error == "" && silentVia(*b, a) != ""
			if silent {
				fix = "The request that triggers the messages failed and none of them arrived. " + unreachableHint +
					": a site that doesn't catch the connector's error fails with it."
			}
			g.add(model.Change{Class: model.Broke, Check: ActionFailed, Title: title, Detail: a.Error, Fix: fix}, a.Label)
			if silent {
				// Nothing arrived at all, so whatever the snapshot had is
				// lost whatever else the failure hides.
				for _, c := range Messages(strip(b.Messages, o.ignored(a)), nil) {
					g.add(c, a.Label)
				}
			}
			continue
		}
		if b != nil && b.Error != "" {
			// There's nothing in the snapshot to compare with; listing all
			// it sends now as NEW would bury the fix.
			g.add(model.Change{Class: model.Fixed, Check: ActionFailed, Title: "Action fails"}, a.Label)
			notes = append(notes, fmt.Sprintf("%q failed in the snapshot, so what it sends now wasn't compared: take a new snapshot", a.Label))
			continue
		}
		if b == nil {
			// Not in the snapshot: everything it sends is new.
			for _, c := range Messages(nil, strip(a.Messages, o.ignored(a))) {
				g.add(c, a.Label)
			}
			for _, c := range Issues(nil, a.Issues) {
				g.add(c, a.Label)
			}
			continue
		}
		ignore := o.ignored(a)
		cs, cut := messages(strip(b.Messages, ignore), strip(a.Messages, ignore), o.coveredText(b.Issues, a.Issues))
		for _, c := range cs {
			g.add(c, a.Label)
		}
		for _, n := range cut {
			if !slices.Contains(notes, n) {
				notes = append(notes, n)
			}
		}
		for _, c := range Issues(b.Issues, a.Issues) {
			g.add(c, a.Label)
		}
	}
	for _, c := range Issues(before.Issues, after.Issues) {
		g.add(c, "site-wide")
	}
	out.Changes = g.list()
	for i, c := range out.Changes {
		out.Changes[i].Diff = modelDiff(ChangeDiff(c))
	}
	out.Warnings = append(mismatch(before, after), notes...)
	if len(o.Accept) > 0 {
		Accept(out, o.Accept)
	}
	return out
}

// ActionFailed is the check of a change about an action that failed in the
// compared run (BROKE) or failed only in the snapshot (FIXED).
const ActionFailed = "run/action-failed"

// Messages diffs the messages one action sent in two runs.
func Messages(before, after []model.Message) []model.Change {
	cs, _ := messages(before, after, nil)
	return cs
}

// messages is Messages, plus notes on what couldn't be compared. cover
// says, by message ID, how much of a text change an issue already reports.
func messages(before, after []model.Message, cover map[string]covered) (out []model.Change, notes []string) {
	bs, as := byIdentity(before), byIdentity(after)
	ids := keys(bs)
	for _, id := range keys(as) {
		if _, ok := bs[id]; !ok {
			ids = append(ids, id)
		}
	}
	for _, id := range ids {
		b, a := bs[id], as[id]
		// Several messages to the same place (a welcome and an account
		// email to one address) are told apart by their subject.
		multi := len(b) > 1 || len(a) > 1
		pairs, lost, added, paired := pair(b, a)
		for _, p := range pairs {
			cs, note := content(p[0], p[1], cover[p[1].ID])
			out = append(out, cs...)
			if note != "" && !slices.Contains(notes, note) {
				notes = append(notes, note)
			}
		}
		switch {
		case len(lost) > 0 && len(a) > 0 && !distinct(lost, paired):
			out = append(out, model.Change{Class: model.Changed, Check: "message/fewer", Channel: lost[0].Channel,
				Title:  fmt.Sprintf("%s is sent fewer times", check.Capitalize(label(lost[0].Message, multi))),
				Before: times(len(b)), After: times(len(a))})
		case len(added) > 0 && len(b) > 0 && !distinct(added, paired):
			// The same message again: a duplicate send (a hook firing twice).
			out = append(out, model.Change{Class: model.Changed, Check: "message/more", Channel: added[0].Channel,
				Title:  fmt.Sprintf("%s is sent more times", check.Capitalize(label(added[0].Message, multi))),
				Fix:    "Something now sends it again: check for a hook or plugin that fires twice.",
				Before: times(len(b)), After: times(len(a))})
			added = nil
		default:
			for _, m := range lost {
				out = append(out, model.Change{Class: model.Broke, Check: "message/missing", Channel: m.Channel,
					Title:  fmt.Sprintf("%s is no longer sent", check.Capitalize(label(m.Message, multi))),
					Fix:    "Something in the change stopped this message: check the plugin or code that sends it and its settings.",
					Before: "sent", After: "not sent"})
			}
		}
		for _, m := range added {
			out = append(out, model.Change{Class: model.New, Check: "message/new", Channel: m.Channel,
				Title: fmt.Sprintf("%s is now sent", check.Capitalize(label(m.Message, multi))), Detail: preview(m.Message)})
		}
	}
	return out, notes
}

// keyed is a message with its pairKey, worked out once: it runs several
// regular expressions over the subject.
type keyed struct {
	model.Message
	key string
}

// pair matches the messages of one identity across runs: the same subject
// (or event) first, then whatever is left in order, so a changed subject
// reads as a change rather than one message lost and another new. paired
// holds the keys of the messages from b that were paired.
func pair(b, a []model.Message) (pairs [][2]model.Message, lost, added []keyed, paired map[string]bool) {
	// Where each key is in a, in order, so every message is looked up once.
	at := map[string][]int{}
	ka := make([]keyed, len(a))
	for i, m := range a {
		ka[i] = keyed{m, keyOf(m)}
		at[ka[i].key] = append(at[ka[i].key], i)
	}
	used := make([]bool, len(a))
	paired = map[string]bool{}
	var rest []keyed
	for _, m := range b {
		k := keyed{m, keyOf(m)}
		if idx := at[k.key]; len(idx) > 0 {
			at[k.key] = idx[1:]
			used[idx[0]] = true
			paired[k.key] = true
			pairs = append(pairs, [2]model.Message{m, a[idx[0]]})
		} else {
			rest = append(rest, k)
		}
	}
	var left []keyed
	for i, n := range ka {
		if !used[i] {
			left = append(left, n)
		}
	}
	for len(rest) > 0 && len(left) > 0 {
		pairs = append(pairs, [2]model.Message{rest[0].Message, left[0].Message})
		paired[rest[0].key] = true
		rest, left = rest[1:], left[1:]
	}
	return pairs, rest, left, paired
}

func pairKey(m model.Message) string { return template(m.Subject) + "|" + m.Event }

// keyOf is pairKey; a test counts its calls.
var keyOf = pairKey

// distinct reports whether the unpaired messages (lost or added) differ
// from the ones sent in both runs; when they don't, the same message is
// simply sent fewer or more times.
func distinct(unpaired []keyed, paired map[string]bool) bool {
	for _, l := range unpaired {
		if !paired[l.key] {
			return true
		}
	}
	return false
}

// label is identityLabel, with the subject when several messages share the
// identity.
func label(m model.Message, multi bool) string {
	l := identityLabel(m)
	if multi && strings.TrimSpace(m.Subject) != "" {
		l += fmt.Sprintf(" (%q)", display(m.Subject))
	}
	return l
}

// truncatedMark ends a field the catcher cut to its size cap (MaxBody,
// 256 KiB by default).
const truncatedMark = "…(truncated)"

// truncated reports whether a message's payload was cut, so its fields
// can't be compared: a cut body is usually no longer valid JSON and
// would read as every field lost.
func truncated(m model.Message) bool { return strings.HasSuffix(m.Body, truncatedMark) }

// content compares two matched messages. note says what couldn't be
// compared. cover leaves out a text change an issue in a already reports:
// a new placeholder or PHP error is BROKE and a new dev-link names the
// host its changed lines carry, so the text change would say it twice.
func content(b, a model.Message, cover covered) (out []model.Change, note string) {
	what := check.Capitalize(identityLabel(a))
	ch := func(check, title, before, after, fix string) {
		out = append(out, model.Change{Class: model.Changed, Check: check, Channel: a.Channel, Title: title, Before: before, After: after, Fix: fix})
	}
	if ok(b) && !ok(a) {
		out = append(out, model.Change{Class: model.Broke, Check: "message/failing", Channel: a.Channel,
			Title: fmt.Sprintf("%s now fails", what), Before: string(b.Status), After: string(a.Status), Detail: a.Error,
			Fix: "The site tried to send it and the send failed: check the sender's credentials and settings."})
		return out, ""
	}
	if b.Channel == model.Email {
		if template(b.Subject) != template(a.Subject) {
			ch("message/subject", what+": subject changed", display(b.Subject), display(a.Subject), "")
		}
		if bf, af := roles(b.From), roles(a.From); bf != af {
			ch("message/from", what+": sender changed", bf, af, "A new From address may fail SPF/DKIM/DMARC alignment; check it with: rhythm echo check <domain>")
		}
		if bc, ac := roles(strings.Join(append(slices.Clone(b.CC), b.BCC...), ",")), roles(strings.Join(append(slices.Clone(a.CC), a.BCC...), ",")); bc != ac {
			ch("message/copies", what+": copies (Cc/Bcc) changed", bc, ac, "")
		}
		if b.ReplyTo != "" && a.ReplyTo == "" {
			ch("message/reply-to", what+": lost its Reply-To", roles(b.ReplyTo), "", "Replies now go to the sender address; restore Reply-To if customers should reach a person.")
		}
	}
	if bt, at := text(b), text(a); !cover.all {
		if h := Diff(bt, at); len(h) > 0 && !cover.says(h) {
			// The full texts, so a report can show the diff (ChangeDiff).
			ch("message/text", what+": text changed", bt, at, "Read the new text; if the change is intended, take a new snapshot.")
		}
	}
	out = append(out, attachments(b, a, what)...)
	if b.Event != a.Event && b.Event != "" {
		ch("message/event", what+": event changed", b.Event, a.Event, "")
	}
	if truncated(b) || truncated(a) {
		return out, what + ": payload too large to compare fields (cut at 256 KiB)"
	}
	bk, ak := fields(b.Body), fields(a.Body)
	if len(bk) > 0 || len(ak) > 0 {
		lost, added := diffSets(bk, ak)
		if len(lost) > 0 {
			// An app or a payment flow reads a payload by field name: one
			// that's gone breaks it, where a person reading an email might
			// not notice.
			class := model.Changed
			if a.Channel == model.Webhook || a.Channel == model.Payment {
				class = model.Broke
			}
			out = append(out, model.Change{Class: class, Check: "message/fields-lost", Channel: a.Channel,
				Title: fmt.Sprintf("%s: payload lost fields", what), Detail: strings.Join(limit(lost, 12), ", "),
				Fix: "Whatever receives this (a CRM, Zapier, an app) may depend on these fields."})
		}
		if len(added) > 0 {
			ch("message/fields-added", what+": payload has new fields", "", strings.Join(limit(added, 12), ", "), "")
		}
		// Same fields, other values: a line diff of the indented JSON
		// shows which. Lost or new fields already say the payload moved.
		if len(lost) == 0 && len(added) == 0 {
			bj, bok := prettyJSON(b.Body)
			aj, aok := prettyJSON(a.Body)
			if bok && aok && len(Diff(bj, aj)) > 0 {
				ch("message/payload", what+": payload changed", bj, aj,
					"Whatever receives this (a CRM, Zapier, an app) now gets other values; if the change is intended, take a new snapshot.")
			}
		}
	}
	return out, ""
}

// Issues diffs issues by key, as Soundcheck does: a new error is BROKE,
// another new issue NEW, one that went away FIXED.
func Issues(before, after []model.Issue) []model.Change {
	var out []model.Change
	seen := map[string]bool{}
	for _, i := range before {
		seen[i.Key] = true
	}
	now := map[string]bool{}
	for _, i := range after {
		now[i.Key] = true
		if seen[i.Key] {
			continue
		}
		class := model.New
		if i.Level == model.Error {
			class = model.Broke
		}
		out = append(out, model.Change{Class: class, Check: i.Check, Title: i.Title, Detail: i.Detail, Fix: i.Fix})
		seen[i.Key] = true
	}
	for _, i := range before {
		if !now[i.Key] {
			out = append(out, model.Change{Class: model.Fixed, Check: i.Check, Title: i.Title})
			now[i.Key] = true
		}
	}
	return out
}

// identity is what makes two messages "the same message" across runs: its
// channel, provider and where it goes (recipient roles for email and SMS,
// host, path pattern and event for HTTP).
func identity(m model.Message) string {
	switch m.Channel {
	case model.Email, model.SMS, model.WhatsApp:
		return string(m.Channel) + "|" + m.Provider + "|" + roles(strings.Join(m.To, ","))
	}
	u, _ := url.Parse(m.URL)
	h, p := "", ""
	if u != nil {
		h, p = u.Hostname(), pathPattern(u.Path)
	}
	return strings.Join([]string{string(m.Channel), m.Provider, m.Method, h, p, m.Event}, "|")
}

// identityLabel is the identity in words, for titles.
func identityLabel(m model.Message) string {
	what := check.Describe(m)
	switch m.Channel {
	case model.Email, model.SMS, model.WhatsApp:
		if r := roles(strings.Join(m.To, ", ")); r != "" {
			return what + " to " + r
		}
	case model.Analytics:
		if m.Event != "" {
			return fmt.Sprintf("%s %q", what, m.Event)
		}
	case model.Webhook:
		if m.Event != "" {
			return fmt.Sprintf("%s (%q)", what, m.Event)
		}
		// One Zap or scenario is its own destination: name the (masked) hook,
		// so a moved hook doesn't read as the same webhook lost and new.
		if u, err := url.Parse(m.URL); err == nil && u.Host != "" && recognize.SecretHook(m.Provider) {
			return what + " to " + u.Host + strings.TrimSuffix(u.Path, "/")
		}
	}
	return what
}

func byIdentity(ms []model.Message) map[string][]model.Message {
	out := map[string][]model.Message{}
	for _, m := range ms {
		k := identity(m)
		out[k] = append(out[k], m)
	}
	return out
}

var (
	testAddr   = regexp.MustCompile(`(?i)(?:soundcheck|echo)-test(\+[^@\s,]*)?@[^\s,>]+`)
	testPhone  = regexp.MustCompile(`\+1555\d{7}|\+15005550\d{3}`)
	digits     = regexp.MustCompile(`#?\d+`) // "#1042" reads "#", not "##"
	space      = regexp.MustCompile(`\s+`)
	tags       = regexp.MustCompile(`<[^>]+>`)
	idSegment  = regexp.MustCompile(`^(\d+|[0-9a-f]{8,}|[0-9a-f-]{36}|[A-Za-z0-9_-]{20,})$`)
	volatileRe = regexp.MustCompile(`(?i)\b(\d{1,2}[:/.-]\d{1,2}([:/.-]\d{2,4})?|[0-9a-f]{12,}|[A-Za-z0-9_-]{24,})\b`)
)

// roles normalizes recipients: test addresses and numbers from
// Soundcheck's fake data become "the test submitter", since they change
// every run; real addresses are kept (the admin address is what matters).
func roles(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = testAddr.ReplaceAllString(s, "the test submitter")
	s = testPhone.ReplaceAllString(s, "the test number")
	return s
}

// template makes a subject comparable: numbers and test data vary per
// run and case doesn't matter. It's a key; show display instead.
func template(s string) string { return strings.ToLower(display(s)) }

// display is a subject as sent, with what varies per run (numbers, test
// data) replaced so the same change across actions reads as one.
func display(s string) string {
	s = strings.TrimSpace(s)
	s = testAddr.ReplaceAllString(s, "the test submitter")
	s = testPhone.ReplaceAllString(s, "the test number")
	s = testAddrName.ReplaceAllString(s, "{name}")
	return space.ReplaceAllString(digits.ReplaceAllString(s, "#"), " ")
}

// testAddrName matches Soundcheck's fake names ("soundcheck-test 3f2a").
var testAddrName = regexp.MustCompile(`(?i)(?:soundcheck|echo)-test[\w -]*`)

// text is what a message says: its plain text, or the visible text of
// its HTML when it has none.
func text(m model.Message) string {
	if strings.TrimSpace(m.Text) != "" {
		return strings.ReplaceAll(m.Text, "\r\n", "\n")
	}
	if m.HTML != "" {
		return visibleText(m.HTML)
	}
	return ""
}

func excerpt(s string) string {
	s = strings.TrimSpace(space.ReplaceAllString(s, " "))
	if r := []rune(s); len(r) > 120 {
		return string(r[:119]) + "…"
	}
	return s
}

func preview(m model.Message) string {
	if m.Subject != "" {
		return "subject: " + m.Subject
	}
	return excerpt(text(m))
}

// pathPattern turns IDs in a URL path into placeholders, so
// /v1/orders/123 and /v1/orders/456 are the same endpoint.
func pathPattern(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		if idSegment.MatchString(s) {
			segs[i] = "{id}"
		}
	}
	return strings.Join(segs, "/")
}

// fields lists the JSON paths in a payload ("order.total",
// "line_items[].sku"); form bodies give their keys.
func fields(body string) []string {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}
	var v any
	if json.Unmarshal([]byte(body), &v) == nil {
		var out []string
		walk(v, "", &out)
		slices.Sort(out)
		return slices.Compact(out)
	}
	if q, err := url.ParseQuery(body); err == nil && len(q) > 0 && !strings.ContainsAny(body, " \n<") {
		var out []string
		for k := range q {
			out = append(out, k)
		}
		slices.Sort(out)
		return out
	}
	return nil
}

func walk(v any, prefix string, out *[]string) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			p := k
			if prefix != "" {
				p = prefix + "." + k
			}
			*out = append(*out, p)
			walk(child, p, out)
		}
	case []any:
		for _, child := range t {
			walk(child, prefix+"[]", out)
		}
	}
}

// diffSets returns what is only in a (lost) and only in b (added). Both
// must be sorted without duplicates, as fields returns them: one merge
// pass then does it in linear time, where looking each key up in the other
// list took seconds for a large payload.
func diffSets(a, b []string) (lost, added []string) {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			i++
			j++
		case a[i] < b[j]:
			lost = append(lost, a[i])
			i++
		default:
			added = append(added, b[j])
			j++
		}
	}
	return append(lost, a[i:]...), append(added, b[j:]...)
}

func ok(m model.Message) bool { return m.Status != model.Failed && m.Status != model.Blocked }

// grouper merges identical changes from several actions into one line.
type grouper struct {
	order []string
	by    map[string]*model.Change
	tune  func(model.Change) (model.Change, bool) // Options.tune; nil keeps every change as it is
}

// groupKey is what makes two changes the same change.
func groupKey(c model.Change) string {
	if strings.HasPrefix(c.Check, "message/attachment-") {
		return attachmentGroupKey(c)
	}
	if hs := ChangeDiff(c); hs != nil {
		// The full texts differ in test data from action to action; the
		// edit is what makes two changes the same.
		return c.Class + "|" + c.Check + "|" + c.Title + "|" + signature(hs)
	}
	return c.Class + "|" + c.Check + "|" + c.Title + "|" + c.Before + "|" + c.After
}

func (g *grouper) add(c model.Change, action string) {
	if g.by == nil {
		g.by = map[string]*model.Change{}
	}
	if g.tune != nil {
		var on bool
		if c, on = g.tune(c); !on {
			return
		}
	}
	k := groupKey(c)
	if have, found := g.by[k]; found {
		if !slices.Contains(have.Actions, action) {
			have.Actions = append(have.Actions, action)
		}
		return
	}
	c.Actions = []string{action}
	g.by[k] = &c
	g.order = append(g.order, k)
}

func (g *grouper) list() []model.Change {
	out := make([]model.Change, 0, len(g.order))
	for _, k := range g.order {
		c := *g.by[k]
		c.Score = float64(len(c.Actions))
		out = append(out, c)
	}
	Sort(out)
	return out
}

// Sort orders changes by class, then by how many actions they affect.
func Sort(cs []model.Change) {
	slices.SortStableFunc(cs, func(x, y model.Change) int {
		return cmp.Or(cmp.Compare(slices.Index(model.Classes, x.Class), slices.Index(model.Classes, y.Class)),
			cmp.Compare(y.Score, x.Score), strings.Compare(x.Title, y.Title))
	})
}

// connectorVias are the connectors that run inside the site. Sealed and
// unable to reach Echo, each blocks every send, so Echo hears nothing.
var connectorVias = []string{"wordpress", "laravel", "node"}

// unreachableHint says why a connector's messages may all be missing.
const unreachableHint = "A connector that can't reach Echo blocks every send while sealed: check the site reaches Echo with rhythm echo doctor"

// silentVia returns the connector that sent b's messages in the snapshot
// when a, the same action now, sent nothing at all, or "".
func silentVia(b, a model.Action) string {
	if len(a.Messages) > 0 {
		return ""
	}
	for _, m := range b.Messages {
		if slices.Contains(connectorVias, m.Source.Via) {
			return m.Source.Via
		}
	}
	return ""
}

// silentConnectors notes each action that sent nothing at all this time
// although a connector in the site sent its mail in the snapshot. Echo
// can't tell that from a site that stopped sending. A connector that
// can't reach Echo is the likelier cause when every message is gone, and
// it can fail the action too: a connector that can't hand a request to
// Echo throws, as a dead connection would.
func silentConnectors(before, after *model.Run) []string {
	var w []string
	for _, b := range before.Actions {
		a := after.Action(b.ID)
		if a == nil || b.Error != "" {
			continue
		}
		via := silentVia(b, *a)
		if via == "" {
			continue
		}
		what := "nothing arrived this time"
		if a.Error != "" {
			what = "the action failed and nothing arrived"
		}
		w = append(w, fmt.Sprintf("%s: %s from the site's %s connector, which sent %s in the snapshot. %s",
			b.Label, what, via, nMessages(len(b.Messages)), unreachableHint))
	}
	return w
}

// nMessages is n messages, in words.
func nMessages(n int) string {
	if n == 1 {
		return "1 message"
	}
	return fmt.Sprintf("%d messages", n)
}

func mismatch(before, after *model.Run) []string {
	var w []string
	if before.Mode != after.Mode {
		w = append(w, fmt.Sprintf("the snapshot ran in %s mode and this run in %s mode", before.Mode, after.Mode))
	}
	missing, added := 0, 0
	for _, a := range before.Actions {
		if after.Action(a.ID) == nil {
			missing++
		}
	}
	for _, a := range after.Actions {
		if before.Action(a.ID) == nil {
			added++
		}
	}
	w = append(w, silentConnectors(before, after)...)
	switch missing {
	case 0:
	case 1:
		w = append(w, "1 action from the snapshot wasn't run this time")
	default:
		w = append(w, fmt.Sprintf("%d actions from the snapshot weren't run this time", missing))
	}
	switch added {
	case 0:
	case 1:
		w = append(w, "1 action isn't in the snapshot; what it sent is listed as new")
	default:
		w = append(w, fmt.Sprintf("%d actions aren't in the snapshot; what they sent is listed as new", added))
	}
	return w
}

// times is a count for before/after: "1 time", "3 times".
func times(n int) string {
	if n == 1 {
		return "1 time"
	}
	return fmt.Sprintf("%d times", n)
}

func keys(m map[string][]model.Message) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func limit(s []string, n int) []string {
	if len(s) > n {
		return append(s[:n:n], fmt.Sprintf("+%d more", len(s)-n))
	}
	return s
}
