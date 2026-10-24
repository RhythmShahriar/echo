// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

package main

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/RhythmShahriar/echo/model"
)

// Tool is another tool the benchmark judges on the same site scenarios,
// in its own scoreboard column.
type Tool interface {
	Name() string  // as -tools and results.json name it, like mailpit
	Title() string // the column's header
	About() string // how it is judged, for the scoreboard
	// Ready reports why the tool can't be judged now, or nil.
	Ready(ctx context.Context) error
	// Judge decides the column for one scenario from what Echo captured
	// in its good and broken states.
	Judge(ctx context.Context, s Scenario, good, broken *model.Run, stamp string) Result
}

// toolNames lists the tools -tools takes, in column order.
var toolNames = []string{"mailpit", "buggregator", "staging-shield"}

// parseTools reads -tools: "auto" (every tool that answers), "none", or
// names separated by spaces or commas, as make benchmark TOOLS="echo
// mailpit buggregator staging-shield" passes them. "echo" is always run
// and may be listed. Named tools are required: the benchmark stops when
// one doesn't answer. It returns the names in column order.
func parseTools(spec string) (names []string, required bool, err error) {
	switch strings.TrimSpace(spec) {
	case "", "auto":
		return toolNames, false, nil
	case "none":
		return nil, false, nil
	}
	for _, f := range strings.FieldsFunc(spec, func(r rune) bool { return r == ',' || r == ' ' }) {
		switch {
		case f == "echo":
		case !slices.Contains(toolNames, f):
			return nil, false, fmt.Errorf("-tools: unknown %q (echo, %s, auto or none)", f, strings.Join(toolNames, ", "))
		case !slices.Contains(names, f):
			names = append(names, f)
		}
	}
	slices.SortFunc(names, func(a, b string) int { return slices.Index(toolNames, a) - slices.Index(toolNames, b) })
	return names, true, nil
}

// inboxMessage is what a mail catcher shows of one message.
type inboxMessage struct {
	Subject, From string
	To            []string
	Text, HTML    string
}

func (m inboxMessage) visible() string {
	return strings.Join([]string{m.From, strings.Join(m.To, ","), m.Subject, m.Text, m.HTML}, "\n")
}

// catcher is a mail catcher the benchmark feeds over SMTP and reads back.
type catcher interface {
	// deliver sends the run's SMTP mail tagged with tag and returns how
	// many it sent.
	deliver(ctx context.Context, r *model.Run, tag string) (int, error)
	// read returns the messages delivered with tag.
	read(ctx context.Context, tag string) ([]inboxMessage, error)
}

// judgeInbox answers, for a mail catcher: would a team that uses it as
// its staging mail server have seen this problem? It catches mail sent
// over SMTP and shows it in an inbox; it doesn't capture API mailers,
// SMS, webhooks or analytics and it keeps no baseline to compare with.
//
// For each state the emails Echo captured that the site would have
// handed to an SMTP server (not those sent through a provider's API) go
// into the catcher, then come back through its API. The scenario counts
// as caught when the inbox itself shows the problem: the manifest's
// `mailpit` text appears in a broken-state message (its From, To,
// Subject or body) and not in the good state's. A lost message is a miss:
// the inbox has one email fewer and nothing says one is missing.
func judgeInbox(ctx context.Context, name string, c catcher, s Scenario, good, broken *model.Run, stamp string) Result {
	if s.Channel != string(model.Email) {
		what := map[string]string{"sms": "SMS", "chat": "chat messages", "analytics": "analytics events", "webhook": "webhooks", "dns": "DNS records"}[s.Channel]
		return Result{Outcome: CantSee, Detail: name + " only receives email; " + what + " never reach it"}
	}
	gt, bt := s.ID+"-good-"+stamp, s.ID+"-broken-"+stamp
	gn, err := c.deliver(ctx, good, gt)
	if err != nil {
		return Result{Outcome: Failed, Detail: err.Error()}
	}
	if gn == 0 {
		return Result{Outcome: CantSee, Detail: "the email goes through a provider's API, not SMTP, so it never reaches " + name}
	}
	gi, err := c.read(ctx, gt)
	if err != nil {
		return Result{Outcome: Failed, Detail: err.Error()}
	}
	if _, err := c.deliver(ctx, broken, bt); err != nil {
		return Result{Outcome: Failed, Detail: err.Error()}
	}
	bi, err := c.read(ctx, bt)
	if err != nil {
		return Result{Outcome: Failed, Detail: err.Error()}
	}
	if s.Mailpit != "" {
		want := strings.ToLower(s.Mailpit)
		inGood := slices.ContainsFunc(gi, func(m inboxMessage) bool { return strings.Contains(strings.ToLower(m.visible()), want) })
		for _, m := range bi {
			if strings.Contains(strings.ToLower(m.visible()), want) && !inGood {
				return Result{Outcome: Caught, Found: fmt.Sprintf("the inbox shows %q in “%s”", s.Mailpit, m.Subject)}
			}
		}
	}
	if len(bi) < len(gi) {
		return Result{Outcome: Missed, Detail: fmt.Sprintf("the inbox has %s instead of %d; %s keeps no baseline, so nothing says one is missing", emails(len(bi)), len(gi), name)}
	}
	return Result{Outcome: Missed, Detail: fmt.Sprintf("%s in the inbox and the problem isn't visible there", emails(len(bi)))}
}

func emails(n int) string {
	if n == 1 {
		return "1 email"
	}
	return fmt.Sprintf("%d emails", n)
}
