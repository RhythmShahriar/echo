// Package compare diffs two runs: for each action that's in both, it
// matches the messages sent before and after, and reports the ones that
// stopped (BROKE), appeared (NEW), changed (who they go to, the subject,
// the text, the fields of a payload) or problems that went away (FIXED).
// The same change across many actions is one line.
package compare

import (
	"cmp"
	"crypto/sha1" //nolint:gosec // fingerprints, not security
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/check"
	"github.com/RhythmShahriar/echo/model"
)

// Compare diffs before and after.
func Compare(before, after *model.Run, beforeDir, afterDir, version string) *model.Comparison {
	out := &model.Comparison{Schema: model.Schema, Tool: "rhythm-echo", Version: version, Created: time.Now().UTC(),
		Before: model.RunRef{Target: before.Target, Name: before.Name, Dir: beforeDir, Started: before.Started},
		After:  model.RunRef{Target: after.Target, Name: after.Name, Dir: afterDir, Started: after.Started}}
	g := grouper{}
	for _, a := range after.Actions {
		b := before.Action(a.ID)
		if b == nil {
			continue
		}
		out.Actions++
		if b.Error == "" && a.Error != "" {
			g.add(model.Change{Class: model.Broke, Check: "action/failed", Title: "Action fails",
				Detail: a.Error, Fix: "The request that triggers the messages failed; fix that first."}, a.Label)
			continue
		}
		for _, c := range Messages(b.Messages, a.Messages) {
			g.add(c, a.Label)
		}
		for _, c := range Issues(b.Issues, a.Issues) {
			g.add(c, a.Label)
		}
	}
	for _, c := range Issues(before.Issues, after.Issues) {
		g.add(c, "site-wide")
	}
	out.Changes = g.list()
	out.Warnings = mismatch(before, after)
	return out
}

// Messages diffs the messages one action sent in two runs.
func Messages(before, after []model.Message) []model.Change {
	var out []model.Change
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
		pairs, lost, added := pair(b, a)
		for _, p := range pairs {
			out = append(out, content(p[0], p[1])...)
		}
		switch {
		case len(lost) > 0 && len(a) > 0 && !distinct(lost, pairs):
			out = append(out, model.Change{Class: model.Changed, Check: "message/fewer", Channel: lost[0].Channel,
				Title:  fmt.Sprintf("%s is sent fewer times", capitalize(label(lost[0], multi))),
				Before: fmt.Sprint(len(b)), After: fmt.Sprint(len(a))})
		default:
			for _, m := range lost {
				out = append(out, model.Change{Class: model.Broke, Check: "message/missing", Channel: m.Channel,
					Title:  fmt.Sprintf("%s is no longer sent", capitalize(label(m, multi))),
					Fix:    "Something in the change stopped this message: check the plugin or code that sends it, and its settings.",
					Before: "sent", After: "not sent"})
			}
		}
		for _, m := range added {
			out = append(out, model.Change{Class: model.New, Check: "message/new", Channel: m.Channel,
				Title: fmt.Sprintf("%s is now sent", capitalize(label(m, multi))), Detail: preview(m)})
		}
	}
	return out
}

// pair matches the messages of one identity across runs: the same subject
// (or event) first, then whatever is left in order, so a changed subject
// reads as a change rather than one message lost and another new.
func pair(b, a []model.Message) (pairs [][2]model.Message, lost, added []model.Message) {
	used := make([]bool, len(a))
	var rest []model.Message
	for _, m := range b {
		found := -1
		for i, n := range a {
			if !used[i] && pairKey(m) == pairKey(n) {
				found = i
				break
			}
		}
		if found >= 0 {
			used[found] = true
			pairs = append(pairs, [2]model.Message{m, a[found]})
		} else {
			rest = append(rest, m)
		}
	}
	var left []model.Message
	for i, n := range a {
		if !used[i] {
			left = append(left, n)
		}
	}
	for len(rest) > 0 && len(left) > 0 {
		pairs = append(pairs, [2]model.Message{rest[0], left[0]})
		rest, left = rest[1:], left[1:]
	}
	return pairs, rest, left
}

func pairKey(m model.Message) string { return template(m.Subject) + "|" + m.Event }

// distinct reports whether the lost messages differ from the ones still
// sent; when they don't, the same message is simply sent fewer times.
func distinct(lost []model.Message, pairs [][2]model.Message) bool {
	for _, l := range lost {
		same := false
		for _, p := range pairs {
			if pairKey(p[0]) == pairKey(l) {
				same = true
				break
			}
		}
		if !same {
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
		l += fmt.Sprintf(" (%q)", strings.TrimSpace(digits.ReplaceAllString(m.Subject, "#")))
	}
	return l
}

// content compares two matched messages.
func content(b, a model.Message) []model.Change {
	var out []model.Change
	what := capitalize(identityLabel(a))
	ch := func(check, title, before, after, fix string) {
		out = append(out, model.Change{Class: model.Changed, Check: check, Channel: a.Channel, Title: title, Before: before, After: after, Fix: fix})
	}
	if ok(b) && !ok(a) {
		out = append(out, model.Change{Class: model.Broke, Check: "message/failing", Channel: a.Channel,
			Title: fmt.Sprintf("%s now fails", what), Before: string(b.Status), After: string(a.Status), Detail: a.Error,
			Fix: "The site tried to send it and the send failed: check the sender's credentials and settings."})
		return out
	}
	if b.Channel == model.Email {
		if bt, at := template(b.Subject), template(a.Subject); bt != at {
			ch("message/subject", what+": subject changed", bt, at, "")
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
	if fingerprint(text(b)) != fingerprint(text(a)) {
		ch("message/text", what+": text changed", excerpt(text(b)), excerpt(text(a)), "Read the new text; if the change is intended, take a new snapshot.")
	}
	if b.Event != a.Event && b.Event != "" {
		ch("message/event", what+": event changed", b.Event, a.Event, "")
	}
	bk, ak := fields(b.Body), fields(a.Body)
	if len(bk) > 0 || len(ak) > 0 {
		lost, added := diffSets(bk, ak)
		if len(lost) > 0 {
			out = append(out, model.Change{Class: model.Changed, Check: "message/fields-lost", Channel: a.Channel,
				Title: fmt.Sprintf("%s: payload lost fields", what), Detail: strings.Join(limit(lost, 12), ", "),
				Fix: "Whatever receives this (a CRM, Zapier, an app) may depend on these fields."})
		}
		if len(added) > 0 {
			ch("message/fields-added", what+": payload has new fields", "", strings.Join(limit(added, 12), ", "), "")
		}
	}
	return out
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
	digits     = regexp.MustCompile(`\d+`)
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

// template makes a subject comparable: numbers and test data vary per run.
func template(s string) string {
	s = roles(s)
	s = testAddrName.ReplaceAllString(s, "{name}")
	return space.ReplaceAllString(digits.ReplaceAllString(s, "#"), " ")
}

// testAddrName matches Soundcheck's fake names ("soundcheck-test 3f2a").
var testAddrName = regexp.MustCompile(`(?:soundcheck|echo)-test[\w -]*`)

func text(m model.Message) string {
	if strings.TrimSpace(m.Text) != "" {
		return m.Text
	}
	if m.HTML != "" {
		return tags.ReplaceAllString(m.HTML, " ")
	}
	return ""
}

// fingerprint hashes text with what varies run to run removed: numbers,
// dates, tokens, test data, whitespace.
func fingerprint(s string) string {
	s = template(volatileRe.ReplaceAllString(s, "~"))
	if strings.TrimSpace(s) == "" {
		return ""
	}
	sum := sha1.Sum([]byte(strings.TrimSpace(s))) //nolint:gosec // not for security
	return hex.EncodeToString(sum[:8])
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

func diffSets(a, b []string) (lost, added []string) {
	for _, x := range a {
		if !slices.Contains(b, x) {
			lost = append(lost, x)
		}
	}
	for _, x := range b {
		if !slices.Contains(a, x) {
			added = append(added, x)
		}
	}
	return lost, added
}

func ok(m model.Message) bool { return m.Status != model.Failed && m.Status != model.Blocked }

// grouper merges identical changes from several actions into one line.
type grouper struct {
	order []string
	by    map[string]*model.Change
}

func (g *grouper) add(c model.Change, action string) {
	if g.by == nil {
		g.by = map[string]*model.Change{}
	}
	k := c.Class + "|" + c.Check + "|" + c.Title + "|" + c.Before + "|" + c.After
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

func mismatch(before, after *model.Run) []string {
	var w []string
	if before.Mode != after.Mode {
		w = append(w, fmt.Sprintf("the snapshot ran in %s mode and this run in %s mode", before.Mode, after.Mode))
	}
	missing := 0
	for _, a := range before.Actions {
		if after.Action(a.ID) == nil {
			missing++
		}
	}
	if missing > 0 {
		w = append(w, fmt.Sprintf("%d actions from the snapshot weren't run this time", missing))
	}
	return w
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

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
