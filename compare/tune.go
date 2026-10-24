// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package compare

import (
	"crypto/sha1" //nolint:gosec // short stable keys, not security
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"path"
	"slices"
	"strings"

	"github.com/RhythmShahriar/echo/check"
	"github.com/RhythmShahriar/echo/model"
)

// Options tunes what a comparison counts: the project's `checks:`
// overrides, each action's `ignore:` patterns and the changes accepted as
// intended. The zero value is the default comparison.
type Options struct {
	// Checks moves a check to another class or level, by check ID:
	// "off" drops it, "broke" or "error" makes it BROKE, "changed" makes
	// it CHANGED and "warning" or "notice" makes it NEW. FIXED stays
	// FIXED. The same map goes to check.Options.Checks for the run.
	Checks map[string]string
	// Ignore lists, per action (by ID or label), glob patterns as in
	// path.Match for what compare leaves out of that action's messages:
	// payload fields ("order.date_modified", "meta_data[].*"; a field's
	// children go with it), headers ("x-request-id") and recipients
	// ("*@agency.example").
	Ignore map[string][]string
	// Accept lists keys (Key) of changes that are intended. Accepted
	// changes are taken out of the comparison and named in its notes.
	Accept []string
}

// Compare diffs before and after with these options.
func (o Options) Compare(before, after *model.Run, beforeDir, afterDir, version string) *model.Comparison {
	return compareRuns(before, after, beforeDir, afterDir, version, o)
}

// ignored returns the action's ignore patterns.
func (o Options) ignored(a model.Action) []string {
	return append(slices.Clone(o.Ignore[a.ID]), o.Ignore[a.Label]...)
}

// tune applies the Checks overrides to a change: it reports false for one
// that is off and otherwise returns it in its class.
func (o Options) tune(c model.Change) (model.Change, bool) {
	set, found := o.Checks[c.Check]
	set = strings.ToLower(set)
	switch {
	case !found:
		return c, true
	case set == check.Off:
		return c, false
	case c.Class == model.Fixed:
		return c, true
	}
	switch set {
	case model.Broke, string(model.Error):
		c.Class = model.Broke
	case model.Changed:
		c.Class = model.Changed
	case string(model.Warning), string(model.Notice), model.New:
		c.Class = model.New
	}
	return c, true
}

// covered says how much of a message's text change an issue already
// reports. all: a new placeholder or PHP error that counts as BROKE says
// the same thing again. hosts: a new dev-link names these hosts, so a
// change only in lines that carry one of them says nothing more.
type covered struct {
	all   bool
	hosts []string
}

// coveredText lists, by message ID, what the new issues of an action
// already say about its messages' text changes.
func (o Options) coveredText(before, after []model.Issue) map[string]covered {
	had := map[string]bool{}
	for _, i := range before {
		had[i.Key] = true
	}
	out := map[string]covered{}
	for _, i := range after {
		if had[i.Key] || i.Message == "" {
			continue
		}
		switch i.Check {
		case "content/placeholder", "content/php-error":
			class := model.New
			if i.Level == model.Error {
				class = model.Broke
			}
			if c, on := o.tune(model.Change{Class: class, Check: i.Check}); on && c.Class == model.Broke {
				cv := out[i.Message]
				cv.all = true
				out[i.Message] = cv
			}
		case "content/dev-link":
			// Turned off, it reports nothing, so the text change stays.
			h := check.DevLinkHost(i.Title)
			if _, on := o.tune(model.Change{Class: model.New, Check: i.Check}); on && h != "" {
				cv := out[i.Message]
				cv.hosts = append(cv.hosts, h)
				out[i.Message] = cv
			}
		}
	}
	return out
}

// says reports whether a new dev-link covers every change in hunks: each
// changed line rewritten into one that carries a flagged host. A hunk that drops more lines than it adds, or adds a line
// without the host, is a change of its own.
func (cv covered) says(hunks []Hunk) bool {
	if len(cv.hosts) == 0 || len(hunks) == 0 {
		return false
	}
	for _, h := range hunks {
		adds, dels := 0, 0
		for _, l := range h {
			switch l.Op {
			case OpAdd:
				adds++
				if !slices.ContainsFunc(cv.hosts, func(host string) bool {
					return strings.Contains(strings.ToLower(l.Text), strings.ToLower(host))
				}) {
					return false
				}
			case OpDel:
				dels++
			}
		}
		if adds == 0 || dels > adds {
			return false
		}
	}
	return true
}

// Key names a change for Accept: 8 hex characters from what makes two
// changes the same (class, check, title and the edit or the values), so
// the key stays the same from run to run until the change itself does.
func Key(c model.Change) string {
	sum := sha1.Sum([]byte(groupKey(c))) //nolint:gosec // not for security
	return hex.EncodeToString(sum[:4])
}

// Accept takes the changes whose Key is in keys out of c and adds a note
// for each ("accepted as intended: …"). It returns the accepted changes
// and the keys that matched nothing, so a caller can warn about a stale
// key.
func Accept(c *model.Comparison, keys []string) (accepted []model.Change, unknown []string) {
	want := map[string]bool{}
	for _, k := range keys {
		want[strings.ToLower(strings.TrimSpace(k))] = true
	}
	used := map[string]bool{}
	kept := c.Changes[:0:0]
	for _, ch := range c.Changes {
		if k := Key(ch); want[k] {
			used[k] = true
			accepted = append(accepted, ch)
			c.Warnings = append(c.Warnings, fmt.Sprintf("accepted as intended: %s (%s)", ch.Title, k))
			continue
		}
		kept = append(kept, ch)
	}
	c.Changes = kept
	for _, k := range slices.Sorted(maps.Keys(want)) {
		if !used[k] {
			unknown = append(unknown, k)
		}
	}
	return accepted, unknown
}

// Checks lists the checks of the changes compare makes itself; the rest
// come from the issues of check, dns and headers.
var Checks = []string{
	ActionFailed, "message/missing", "message/failing", "message/new", "message/fewer", "message/more",
	"message/subject", "message/from", "message/copies", "message/reply-to", "message/text", "message/event",
	"message/fields-lost", "message/fields-added", "message/payload",
	"message/attachment-lost", "message/attachment-changed", "message/attachment-new",
}

// UnknownChecks returns the IDs in overrides that no check has, sorted,
// so a typo in `checks:` can be pointed out. rules are the custom rules.
func UnknownChecks(overrides map[string]string, rules []check.Rule) []string {
	var out []string
	for id := range overrides {
		_, builtin := check.Lookup(id)
		switch {
		case builtin, slices.Contains(Checks, id),
			slices.ContainsFunc(rules, func(r check.Rule) bool { return r.ID == id }),
			slices.ContainsFunc(check.IssuePrefixes, func(p string) bool { return strings.HasPrefix(id, p) }):
		default:
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

// strip returns messages without what patterns ignore: matching
// recipients, headers and payload fields. Messages are copied; the runs
// are left as they are.
func strip(ms []model.Message, patterns []string) []model.Message {
	if len(patterns) == 0 {
		return ms
	}
	out := make([]model.Message, len(ms))
	for i, m := range ms {
		m.To, m.CC, m.BCC = keepRecipients(m.To, patterns), keepRecipients(m.CC, patterns), keepRecipients(m.BCC, patterns)
		if len(m.Headers) > 0 {
			h := make(map[string]string, len(m.Headers))
			for k, v := range m.Headers {
				if !matchAny(patterns, strings.ToLower(k)) {
					h[k] = v
				}
			}
			m.Headers = h
		}
		if !truncated(m) {
			m.Body = stripFields(m.Body, patterns)
		}
		out[i] = m
	}
	return out
}

func keepRecipients(rs []string, patterns []string) []string {
	var out []string
	for _, r := range rs {
		if !matchAny(patterns, strings.ToLower(strings.TrimSpace(r))) {
			out = append(out, r)
		}
	}
	return out
}

// matchAny reports whether a pattern matches s. "[]" in a pattern is the
// list marker of a field path ("items[].sku"), not an empty class. A bad
// pattern matches nothing (the config checks them when it loads).
func matchAny(patterns []string, s string) bool {
	for _, p := range patterns {
		p = strings.ReplaceAll(strings.ToLower(p), "[]", `\[\]`)
		if ok, _ := path.Match(p, s); ok {
			return true
		}
	}
	return false
}

// fieldIgnored reports whether a field path ("order.billing.email",
// "items[].sku") or one of its parents matches a pattern.
func fieldIgnored(patterns []string, field string) bool {
	for p := field; ; {
		if matchAny(patterns, strings.ToLower(p)) {
			return true
		}
		if q := strings.TrimSuffix(p, "[]"); q != p {
			p = q // "items[]" is the list "items"
			continue
		}
		i := strings.LastIndex(p, ".")
		if i < 0 {
			return false
		}
		p = p[:i]
	}
}

// stripFields drops ignored fields from a JSON or form body. A body that
// loses nothing is returned as it was.
func stripFields(body string, patterns []string) string {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return body
	}
	dec := json.NewDecoder(strings.NewReader(trimmed))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) == nil && !dec.More() {
		if !dropFields(v, "", patterns) {
			return body
		}
		data, err := json.Marshal(v)
		if err != nil {
			return body
		}
		return string(data)
	}
	if q, err := url.ParseQuery(trimmed); err == nil && len(q) > 0 && !strings.ContainsAny(trimmed, " \n<") {
		dropped := false
		for k := range q {
			if fieldIgnored(patterns, k) {
				q.Del(k)
				dropped = true
			}
		}
		if dropped {
			return q.Encode()
		}
	}
	return body
}

// dropFields deletes ignored keys from a decoded JSON value, naming
// paths as fields does. It reports whether it deleted any.
func dropFields(v any, prefix string, patterns []string) bool {
	dropped := false
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			p := k
			if prefix != "" {
				p = prefix + "." + k
			}
			if fieldIgnored(patterns, p) {
				delete(t, k)
				dropped = true
				continue
			}
			dropped = dropFields(child, p, patterns) || dropped
		}
	case []any:
		for _, child := range t {
			dropped = dropFields(child, prefix+"[]", patterns) || dropped
		}
	}
	return dropped
}
