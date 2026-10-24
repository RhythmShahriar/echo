// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package check

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"text/tabwriter"

	"github.com/RhythmShahriar/echo/model"
)

// Rule is one check of a message. Its ID is what issues carry as their
// check, what `checks:` in the config tunes and what `rhythm echo checks`
// lists.
type Rule struct {
	ID       string          // "content/placeholder"
	Level    model.Level     // the level of its issues, unless Run sets another (sms/parts has two)
	Channels []model.Channel // the channels it looks at; none means all
	Doc      string          // one sentence on what it finds
	// Run returns what the rule finds in m. An issue left without a check
	// or a level gets the rule's; Message sets the message ID and key.
	Run func(m model.Message, o Options) []model.Issue
}

// Applies reports whether the rule looks at messages on channel c.
func (r Rule) Applies(c model.Channel) bool {
	return len(r.Channels) == 0 || slices.Contains(r.Channels, c)
}

var (
	registryMu sync.RWMutex
	registry   []Rule
)

// Register adds a rule that Message runs on every message, after the ones
// registered before it. It panics on a rule without an ID or Run, or with
// an ID that is taken: both are programming errors.
func Register(r Rule) {
	registryMu.Lock()
	defer registryMu.Unlock()
	switch {
	case r.ID == "" || r.Run == nil:
		panic("check: Register needs a rule with an ID and a Run")
	case slices.ContainsFunc(registry, func(have Rule) bool { return have.ID == r.ID }):
		panic("check: rule " + r.ID + " is registered twice")
	}
	registry = append(registry, r)
}

// Rules returns the registered rules in the order Message runs them.
func Rules() []Rule {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return slices.Clone(registry)
}

// Lookup returns the registered rule with id.
func Lookup(id string) (Rule, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	i := slices.IndexFunc(registry, func(r Rule) bool { return r.ID == id })
	if i < 0 {
		return Rule{}, false
	}
	return registry[i], true
}

// The built-in rules, in the order their issues were always listed: a
// failed send first, then what the reader sees, then each channel's own.
func init() {
	for _, r := range []Rule{
		sendFailed,
		placeholder, phpErrorRule, encoding, devLink,
		noSubject, emptyEmail, noText, clipped, fromDomain,
		smsNumber, smsParts,
		webhookJSON, webhookSignature,
		secretInBody,
		realRecipientRule,
	} {
		Register(r)
	}
}

// WriteRules lists rules for `rhythm echo checks`: ID, level, channels and
// what each finds, one per line.
func WriteRules(w io.Writer, rules []Rule) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CHECK\tLEVEL\tCHANNELS\tFINDS")
	for _, r := range rules {
		chans := "all"
		if len(r.Channels) > 0 {
			var cs []string
			for _, c := range r.Channels {
				cs = append(cs, string(c))
			}
			chans = strings.Join(cs, ",")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.ID, r.Level, chans, r.Doc)
	}
	return tw.Flush()
}

// readable are the channels people read, where content checks apply.
var readable = []model.Channel{model.Email, model.SMS, model.WhatsApp, model.Chat}
