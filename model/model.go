// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package model holds what Echo records: the messages a site sends
// (email, SMS, webhooks, chat alerts, analytics and payment calls), the
// actions that made it send them, and the issues and changes found when
// checking and comparing runs. Runs are stored as JSON (schema
// rhythm-echo/v1).
package model

import (
	"crypto/sha1" //nolint:gosec // short stable IDs, not security
	"encoding/hex"
	"time"
)

// Schema identifies the JSON layout of stored runs and comparisons.
const Schema = "rhythm-echo/v1"

// Channel is what kind of message something is.
type Channel string

const (
	Email     Channel = "email"
	SMS       Channel = "sms"
	WhatsApp  Channel = "whatsapp"
	Webhook   Channel = "webhook"   // Zapier, Make, CRMs, WooCommerce webhooks, anything else posted to an API
	Chat      Channel = "chat"      // Slack, Discord, Teams
	Analytics Channel = "analytics" // GA4 Measurement Protocol, Meta Conversions API
	Payment   Channel = "payment"   // Stripe, PayPal API calls
	Push      Channel = "push"      // FCM
	HTTP      Channel = "http"      // outbound HTTP Echo couldn't classify
)

// Channels lists channels in report order.
var Channels = []Channel{Email, SMS, WhatsApp, Chat, Webhook, Analytics, Payment, Push, HTTP}

// Status says what happened to a message.
type Status string

const (
	Sent    Status = "sent"    // observed and delivered (observe mode)
	Faked   Status = "faked"   // Echo answered instead of the real service: sealed, or mail caught by Echo's SMTP sink in any mode
	Blocked Status = "blocked" // sealed and refused (no fake available)
	Failed  Status = "failed"  // the site tried and the send failed
)

// Message is one thing the site sent.
type Message struct {
	ID       string            `json:"id"`
	Run      string            `json:"run,omitempty"`    // from X-Echo-Run: the run…
	Action   string            `json:"action,omitempty"` // …and the action that caused it
	Channel  Channel           `json:"channel"`
	Provider string            `json:"provider,omitempty"` // twilio, sendgrid, slack, ga4, woocommerce…
	From     string            `json:"from,omitempty"`
	To       []string          `json:"to,omitempty"`
	CC       []string          `json:"cc,omitempty"`
	BCC      []string          `json:"bcc,omitempty"`
	ReplyTo  string            `json:"reply_to,omitempty"`
	Subject  string            `json:"subject,omitempty"`
	Text     string            `json:"text,omitempty"`
	HTML     string            `json:"html,omitempty"`
	Event    string            `json:"event,omitempty"` // analytics event, webhook topic: "purchase", "order.created"
	Method   string            `json:"method,omitempty"`
	URL      string            `json:"url,omitempty"` // secrets redacted
	Headers  map[string]string `json:"headers,omitempty"`
	Body     string            `json:"body,omitempty"` // raw HTTP or MIME body, secrets redacted, size-capped
	Response *Response         `json:"response,omitempty"`
	Status   Status            `json:"status"`
	Error    string            `json:"error,omitempty"`
	Source   Source            `json:"source"`
	At       time.Time         `json:"at"`
}

// Response is what the remote service (or Echo's fake) answered.
type Response struct {
	Status int    `json:"status"`
	Body   string `json:"body,omitempty"`
}

// Source says how Echo saw a message.
type Source struct {
	Via    string `json:"via"`              // smtp, sendmail, wordpress, laravel, node, proxy
	Stack  string `json:"stack,omitempty"`  // wordpress, laravel, node, php…
	Plugin string `json:"plugin,omitempty"` // the plugin or package that sent it, when known
	Hook   string `json:"hook,omitempty"`   // wp_mail, pre_http_request, fetch, nodemailer…
	Host   string `json:"host,omitempty"`   // the site that sent it
}

// NewID returns a short ID for a message.
func NewID(seed string) string {
	sum := sha1.Sum([]byte(seed)) //nolint:gosec // not for security
	return hex.EncodeToString(sum[:6])
}

// Level says how bad an issue is. In a comparison a new error is BROKE,
// a new warning or notice NEW.
type Level string

const (
	Error   Level = "error"
	Warning Level = "warning"
	Notice  Level = "notice"
)

// Rank orders levels, most serious first.
func (l Level) Rank() int {
	switch l {
	case Error:
		return 0
	case Warning:
		return 1
	}
	return 2
}

// Issue is a problem found in a message or an action.
type Issue struct {
	Check   string   `json:"check"` // "content/merge-tag", "seal/leak"…
	Level   Level    `json:"level"`
	Title   string   `json:"title"` // stable across runs: compare keys on it
	Detail  string   `json:"detail,omitempty"`
	Fix     string   `json:"fix,omitempty"`
	Message string   `json:"message,omitempty"` // the message ID it concerns
	Key     string   `json:"key"`
	Actions []string `json:"actions,omitempty"` // set when grouped
}

// Identify sets the issue's comparison key.
func (i *Issue) Identify() { i.Key = i.Check + "|" + i.Title }

// Action is something that made the site send messages: a form submit, a
// flow step, a replayed webhook, an HTTP request or a command.
type Action struct {
	ID       string    `json:"id"`       // stable across runs: what compare matches on
	Label    string    `json:"label"`    // "Contact form on /contact/"
	Kind     string    `json:"kind"`     // form, flow, replay, http, command, manual
	Messages []Message `json:"messages"` // in the order they were sent
	Issues   []Issue   `json:"issues,omitempty"`
	Error    string    `json:"error,omitempty"` // the action itself failed (the request errored)
	Duration float64   `json:"duration_ms"`
}

// Run is one check of what a site sends.
type Run struct {
	Schema   string    `json:"schema"`
	Tool     string    `json:"tool"`
	Version  string    `json:"version"`
	ID       string    `json:"id"`
	Target   string    `json:"target,omitempty"`
	Name     string    `json:"name,omitempty"` // snapshot name
	Mode     string    `json:"mode"`           // seal or observe
	Started  time.Time `json:"started"`
	Duration float64   `json:"duration_ms"`
	Actions  []Action  `json:"actions"`
	Issues   []Issue   `json:"issues,omitempty"` // run-wide (seal health, deliverability)
	Warnings []string  `json:"warnings,omitempty"`
}

// Action returns the action with id, or nil.
func (r *Run) Action(id string) *Action {
	for i := range r.Actions {
		if r.Actions[i].ID == id {
			return &r.Actions[i]
		}
	}
	return nil
}

// Change classes in a comparison, most urgent first (as in Soundcheck).
const (
	Broke   = "broke"   // a message that was sent before isn't any more, or now fails
	New     = "new"     // a message or a problem that wasn't there before
	Changed = "changed" // the same message, but different: recipient, subject, payload
	Fixed   = "fixed"   // a problem from the snapshot is gone
)

// Classes lists change classes in report order.
var Classes = []string{Broke, New, Changed, Fixed}

// Change is one difference between a snapshot and the current run.
type Change struct {
	Class   string   `json:"class"`
	Check   string   `json:"check"`
	Title   string   `json:"title"`
	Detail  string   `json:"detail,omitempty"`
	Fix     string   `json:"fix,omitempty"`
	Actions []string `json:"actions"` // action labels
	Channel Channel  `json:"channel,omitempty"`
	Before  string   `json:"before,omitempty"`
	After   string   `json:"after,omitempty"`
	Score   float64  `json:"score,omitempty"`
}

// Comparison is the result of `compare`.
type Comparison struct {
	Schema   string    `json:"schema"`
	Tool     string    `json:"tool"`
	Version  string    `json:"version"`
	Before   RunRef    `json:"before"`
	After    RunRef    `json:"after"`
	Changes  []Change  `json:"changes"`
	Actions  int       `json:"actions"`
	Warnings []string  `json:"warnings,omitempty"`
	Created  time.Time `json:"created"`
}

// RunRef points at a stored run.
type RunRef struct {
	Target  string    `json:"target,omitempty"`
	Name    string    `json:"name,omitempty"`
	Dir     string    `json:"dir"`
	Started time.Time `json:"started"`
}

// Count returns how many changes are in a class.
func (c *Comparison) Count(class string) int {
	n := 0
	for _, ch := range c.Changes {
		if ch.Class == class {
			n++
		}
	}
	return n
}
