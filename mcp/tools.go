// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package mcp

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/compare"
	"github.com/RhythmShahriar/echo/dns"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/runner"
	"github.com/RhythmShahriar/echo/store"
)

// keepRuns is how many compare and run folders are kept, as the CLI does.
const keepRuns = 20

// tool is one entry of tools/list.
type tool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations"`
}

func object(required []string, props map[string]any) map[string]any {
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

// annotations are the hints clients show before a call. The tools that
// run actions reach the site (open world); snapshot replaces a snapshot
// of the same name.
func annotations(readOnly, idempotent, destructive bool) map[string]any {
	return map[string]any{"readOnlyHint": readOnly, "destructiveHint": destructive, "idempotentHint": idempotent,
		"openWorldHint": !readOnly}
}

var channelNames = func() []string {
	var out []string
	for _, c := range model.Channels {
		out = append(out, string(c))
	}
	return out
}()

const sinceDesc = `Only messages captured since then: a duration back from now ("15m", "2h") or an RFC 3339 time.`

func toolList() []tool {
	actions := map[string]any{"type": "array", "items": map[string]any{"type": "string"},
		"description": "Names of actions in .echo/config.yaml to run; all of them when empty."}
	return []tool{
		{Name: "list_messages", Title: "List captured messages",
			Description: "Lists what the site sent that Echo captured (email, SMS, webhooks, chat, analytics, payments), newest first: " +
				"one line of fields per message. Use get_message for the whole message.",
			InputSchema: object(nil, map[string]any{
				"channel": map[string]any{"type": "string", "enum": channelNames, "description": "Only this channel."},
				"to":      str("Only messages to a recipient containing this text (case-insensitive)."),
				"run":     str("Only messages from this run ID."),
				"action":  str("Only messages from this action ID (as in a run: action:checkout)."),
				"since":   str(sinceDesc),
				"limit":   map[string]any{"type": "integer", "minimum": 1, "maximum": 200, "description": "At most this many (default 20)."},
			}),
			Annotations: annotations(true, true, false)},
		{Name: "get_message", Title: "Get one message",
			Description: `Returns one captured message with every field: recipients, subject, text, HTML, headers, body and Echo's answer. "latest" is the newest.`,
			InputSchema: object([]string{"id"}, map[string]any{"id": str(`The message ID from list_messages, or "latest".`)}),
			Annotations: annotations(true, true, false)},
		{Name: "latest_code", Title: "Latest one-time code",
			Description: "Finds the newest email or SMS with a one-time code (login, sign-up, 2FA) and returns the code alone.",
			InputSchema: object(nil, map[string]any{
				"to":      str("Only messages to a recipient containing this text."),
				"channel": map[string]any{"type": "string", "enum": []string{"email", "sms", "whatsapp"}, "description": "Only this channel."},
				"since":   str(sinceDesc),
			}),
			Annotations: annotations(true, true, false)},
		{Name: "run_action", Title: "Run actions",
			Description: "Runs actions from .echo/config.yaml against the site (form posts, commands, replayed webhooks) and returns what each sent and the issues found.",
			InputSchema: object(nil, map[string]any{"actions": actions}),
			Annotations: annotations(false, false, false)},
		{Name: "snapshot", Title: "Take a snapshot",
			Description: "Runs the actions and saves what they sent as a snapshot, the baseline compare checks against. Take one before a change.",
			InputSchema: object(nil, map[string]any{
				"name":    str("Snapshot name: letters, digits, dots, dashes and underscores (default: the time)."),
				"actions": actions,
			}),
			Annotations: annotations(false, false, true)},
		{Name: "compare", Title: "Compare with a snapshot",
			Description: "Runs the actions again and compares what they sent with a snapshot: what broke, what is new, what changed, what was fixed.",
			InputSchema: object(nil, map[string]any{
				"snapshot": str("Snapshot name (default: the latest)."),
				"actions":  actions,
			}),
			Annotations: annotations(false, false, false)},
		{Name: "check_domain", Title: "Check a sending domain",
			Description: "Checks the DNS records mail from a domain depends on (SPF, DKIM, DMARC, MX, MTA-STS, TLS-RPT, BIMI) and returns them with the issues found.",
			InputSchema: object([]string{"domain"}, map[string]any{
				"domain":   str("The domain mail comes from, like shop.com."),
				"provider": str("The service that sends its mail, when known: sendgrid, mailgun, ses, postmark…"),
			}),
			// Read-only, though it asks DNS servers out there.
			Annotations: map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true}},
	}
}

// toolError is a failure the agent should read (a tool result with
// isError), not a protocol error.
type toolError struct{ msg string }

func (e toolError) Error() string { return e.msg }

func toolErrorf(format string, a ...any) error { return toolError{fmt.Sprintf(format, a...)} }

// call runs a tool. Unknown tools and arguments that don't fit the schema
// are protocol errors; anything that goes wrong while running is a tool
// result with isError, so the agent sees why.
func (s *Server) call(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	var (
		v   any
		err error
	)
	switch name {
	case "list_messages":
		var a struct {
			Channel, To, Run, Action, Since string
			Limit                           int
		}
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		v, err = s.listMessages(ctx, a.Channel, a.To, a.Run, a.Action, a.Since, a.Limit)
	case "get_message":
		var a struct{ ID string }
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		v, err = s.getMessage(ctx, a.ID)
	case "latest_code":
		var a struct{ To, Channel, Since string }
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		// The code is the answer: it is returned as is and nothing else
		// from the message is.
		code, id, err := s.latestCode(ctx, a.To, a.Channel, a.Since)
		if err != nil {
			return failed(err)
		}
		return result(map[string]any{"code": code, "message_id": id})
	case "run_action":
		var a struct{ Actions []string }
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		v, err = s.runActions(ctx, a.Actions)
	case "snapshot":
		var a struct {
			Name    string
			Actions []string
		}
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		v, err = s.snapshot(ctx, a.Name, a.Actions)
	case "compare":
		var a struct {
			Snapshot string
			Actions  []string
		}
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		v, err = s.compare(ctx, a.Snapshot, a.Actions)
	case "check_domain":
		var a struct{ Domain, Provider string }
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		v, err = s.checkDomain(ctx, a.Domain, a.Provider)
	default:
		return nil, errorf(codeInvalidParams, "no tool %q", name)
	}
	if err != nil {
		return failed(err)
	}
	return result(Mask(v))
}

// decode reads tool arguments, refusing unknown ones. Keys match the
// fields case-insensitively, so snake_case needs no tags here.
func decode(raw json.RawMessage, v any) error {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errorf(codeInvalidParams, "arguments: %v", err)
	}
	return nil
}

// result is a tools/call answer: the JSON as text for every client and
// as structuredContent for those that read it.
func result(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	structured := v
	if _, ok := v.(map[string]any); !ok {
		structured = map[string]any{"result": v}
	}
	return map[string]any{"content": []map[string]any{{"type": "text", "text": string(b)}},
		"structuredContent": structured, "isError": false}, nil
}

func failed(err error) (any, error) {
	if errors.Is(err, context.Canceled) {
		return nil, errorf(codeInternal, "canceled")
	}
	return map[string]any{"content": []map[string]any{{"type": "text", "text": maskText(err.Error())}}, "isError": true}, nil
}

// since turns a duration back from now or an RFC 3339 time into a time.
func (s *Server) since(v string) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return s.Now().Add(-d), nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	return time.Time{}, toolErrorf(`since %q: use a duration like "15m" or an RFC 3339 time`, v)
}

// summary is one line of list_messages.
type summary struct {
	ID       string        `json:"id"`
	At       time.Time     `json:"at"`
	Channel  model.Channel `json:"channel"`
	Provider string        `json:"provider,omitempty"`
	Status   model.Status  `json:"status"`
	From     string        `json:"from,omitempty"`
	To       []string      `json:"to,omitempty"`
	Subject  string        `json:"subject,omitempty"`
	Event    string        `json:"event,omitempty"`
	Method   string        `json:"method,omitempty"`
	URL      string        `json:"url,omitempty"`
	Run      string        `json:"run,omitempty"`
	Action   string        `json:"action,omitempty"`
	Error    string        `json:"error,omitempty"`
}

func summarize(m model.Message) summary {
	return summary{ID: m.ID, At: m.At, Channel: m.Channel, Provider: m.Provider, Status: m.Status, From: m.From, To: m.To,
		Subject: m.Subject, Event: m.Event, Method: m.Method, URL: m.URL, Run: m.Run, Action: m.Action, Error: m.Error}
}

func (s *Server) listMessages(ctx context.Context, channel, to, run, action, since string, limit int) (any, error) {
	if channel != "" && !slices.Contains(channelNames, channel) {
		return nil, toolErrorf("channel %q: use one of %s", channel, strings.Join(channelNames, ", "))
	}
	t, err := s.since(since)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	limit = min(limit, 200)
	ms, err := s.Echo.Messages(ctx, client.Filter{Channel: model.Channel(channel), To: to, Run: run, Action: action, Since: t})
	if err != nil {
		return nil, err
	}
	slices.Reverse(ms)
	out := map[string]any{"total": len(ms), "messages": []summary{}}
	list := []summary{}
	for _, m := range ms[:min(len(ms), limit)] {
		list = append(list, summarize(m))
	}
	out["messages"] = list
	return out, nil
}

func (s *Server) getMessage(ctx context.Context, id string) (any, error) {
	if id == "" {
		return nil, toolErrorf("id is empty; take one from list_messages")
	}
	if id == "latest" {
		ms, err := s.Echo.Messages(ctx, client.Filter{})
		if err != nil {
			return nil, err
		}
		if len(ms) == 0 {
			return nil, toolErrorf("Echo has captured nothing yet")
		}
		return ms[len(ms)-1], nil
	}
	m, err := s.Echo.Message(ctx, id)
	if client.IsNotFound(err) {
		return nil, toolErrorf("no message %q in Echo; list_messages shows what there is", id)
	}
	return m, err
}

func (s *Server) latestCode(ctx context.Context, to, channel, since string) (string, string, error) {
	t, err := s.since(since)
	if err != nil {
		return "", "", err
	}
	ms, err := s.Echo.Messages(ctx, client.Filter{To: to, Channel: model.Channel(channel), Since: t})
	if err != nil {
		return "", "", err
	}
	looked := 0
	for i := len(ms) - 1; i >= 0; i-- {
		m := ms[i]
		if m.Channel != model.Email && m.Channel != model.SMS && m.Channel != model.WhatsApp {
			continue
		}
		looked++
		if code := Code(m); code != "" {
			return code, m.ID, nil
		}
	}
	return "", "", toolErrorf("no one-time code in the %d emails and texts that match", looked)
}

// run runs the selected actions while holding the actions lock.
func (s *Server) run(ctx context.Context, name string, only []string) (*model.Run, error) {
	if _, err := runner.Select(s.Config, only); err != nil {
		return nil, toolError{err.Error()}
	}
	s.actions.Lock()
	defer s.actions.Unlock()
	return runner.Run(ctx, runner.Options{Config: s.Config, Client: s.Echo, Version: s.Version, Name: name, Only: only, Replay: s.Replay})
}

// runView is a run as the tools return it: actions with what they sent
// (one line each) and the issues found.
type runView struct {
	RunID    string       `json:"run_id"`
	Name     string       `json:"name,omitempty"`
	Mode     string       `json:"mode"`
	Dir      string       `json:"dir,omitempty"`
	Actions  []actionView `json:"actions"`
	Issues   []issueView  `json:"issues,omitempty"`
	Warnings []string     `json:"warnings,omitempty"`
}

type actionView struct {
	ID       string      `json:"id"`
	Label    string      `json:"label"`
	Kind     string      `json:"kind"`
	Error    string      `json:"error,omitempty"`
	Messages []summary   `json:"messages"`
	Issues   []issueView `json:"issues,omitempty"`
}

type issueView struct {
	Level   model.Level `json:"level"`
	Check   string      `json:"check"`
	Title   string      `json:"title"`
	Detail  string      `json:"detail,omitempty"`
	Fix     string      `json:"fix,omitempty"`
	Message string      `json:"message,omitempty"`
}

func issues(is []model.Issue) []issueView {
	var out []issueView
	for _, i := range is {
		out = append(out, issueView{Level: i.Level, Check: i.Check, Title: i.Title, Detail: i.Detail, Fix: i.Fix, Message: i.Message})
	}
	return out
}

func view(r *model.Run, dir string) runView {
	v := runView{RunID: r.ID, Name: r.Name, Mode: r.Mode, Dir: dir, Actions: []actionView{}, Issues: issues(r.Issues), Warnings: r.Warnings}
	for _, a := range r.Actions {
		av := actionView{ID: a.ID, Label: a.Label, Kind: a.Kind, Error: a.Error, Messages: []summary{}, Issues: issues(a.Issues)}
		for _, m := range a.Messages {
			av.Messages = append(av.Messages, summarize(m))
		}
		v.Actions = append(v.Actions, av)
	}
	return v
}

func (s *Server) runActions(ctx context.Context, only []string) (any, error) {
	dir, err := s.Store.NewDir("runs", store.Stamp(s.Now()))
	if err != nil {
		return nil, err
	}
	r, err := s.run(ctx, "", only)
	if r == nil {
		_ = os.Remove(dir)
		return nil, err
	}
	if err := store.SaveRun(dir, r); err != nil {
		return nil, err
	}
	s.Store.ExcludeFromGit()
	_ = s.Store.Prune(keepRuns)
	return view(r, dir), err
}

func (s *Server) snapshot(ctx context.Context, name string, only []string) (any, error) {
	if name != "" && !store.ValidName(name) {
		return nil, toolErrorf("name %q: use letters, digits, dots, dashes and underscores", name)
	}
	name = cmp.Or(name, store.Stamp(s.Now()))
	if _, err := runner.Select(s.Config, only); err != nil {
		return nil, toolError{err.Error()}
	}
	dir, err := s.Store.NewDir(store.Incoming, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }() //nolint:gosec // our own incoming folder
	r, err := s.run(ctx, name, only)
	if err != nil {
		return nil, err // a partial run is no baseline
	}
	if err := store.SaveRun(dir, r); err != nil {
		return nil, err
	}
	final, err := s.Store.Promote(dir, name)
	if err == nil {
		err = s.Store.SetLatest(name)
	}
	if err != nil {
		return nil, err
	}
	s.Store.ExcludeFromGit()
	return view(r, final), nil
}

// comparisonView is what compare returns: the verdict, the counts and
// every change, plus the run it compared.
type comparisonView struct {
	Verdict  string         `json:"verdict"`
	Snapshot string         `json:"snapshot"`
	Counts   map[string]int `json:"counts"`
	Changes  []changeView   `json:"changes"`
	Warnings []string       `json:"warnings,omitempty"`
	Run      runView        `json:"run"`
}

type changeView struct {
	Class   string   `json:"class"`
	Check   string   `json:"check"`
	Title   string   `json:"title"`
	Detail  string   `json:"detail,omitempty"`
	Fix     string   `json:"fix,omitempty"`
	Actions []string `json:"actions"`
	Channel string   `json:"channel,omitempty"`
	Before  string   `json:"before,omitempty"`
	After   string   `json:"after,omitempty"`
}

// Verdict sums up a comparison in one sentence.
func Verdict(c *model.Comparison) string {
	switch {
	case c.Count(model.Broke) > 0:
		return "Something broke."
	case c.Count(model.New) > 0 || c.Count(model.Changed) > 0:
		return "Something changed."
	case c.Count(model.Fixed) > 0:
		return "Only fixes: nothing broke or changed."
	}
	return "Nothing changed."
}

func (s *Server) compare(ctx context.Context, snapshot string, only []string) (any, error) {
	snapDir, err := s.Store.Snapshot(snapshot)
	if err != nil {
		return nil, toolError{err.Error() + "; take one with the snapshot tool"}
	}
	if _, err := runner.Select(s.Config, only); err != nil {
		return nil, toolError{err.Error()}
	}
	before, err := store.LoadRun(snapDir)
	if err != nil {
		return nil, err
	}
	dir, err := s.Store.NewDir("runs", store.Stamp(s.Now()))
	if err != nil {
		return nil, err
	}
	after, err := s.run(ctx, "", only)
	if after == nil {
		_ = os.Remove(dir)
		return nil, err
	}
	if err := store.SaveRun(dir, after); err != nil {
		return nil, err
	}
	c := compare.Compare(before, after, snapDir, dir, s.Version)
	c.Warnings = append(c.Warnings, after.Warnings...)
	if err := store.SaveComparison(dir, c); err != nil {
		return nil, err
	}
	s.Store.ExcludeFromGit()
	_ = s.Store.Prune(keepRuns)
	v := comparisonView{Verdict: Verdict(c), Snapshot: before.Name, Counts: map[string]int{}, Changes: []changeView{},
		Warnings: c.Warnings, Run: view(after, dir)}
	for _, class := range model.Classes {
		v.Counts[class] = c.Count(class)
	}
	for _, ch := range c.Changes {
		v.Changes = append(v.Changes, changeView{Class: ch.Class, Check: ch.Check, Title: ch.Title, Detail: ch.Detail, Fix: ch.Fix,
			Actions: ch.Actions, Channel: string(ch.Channel), Before: ch.Before, After: ch.After})
	}
	return v, err
}

func (s *Server) checkDomain(ctx context.Context, domain, provider string) (any, error) {
	domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
	if i := strings.LastIndex(domain, "@"); i >= 0 {
		domain = domain[i+1:]
	}
	if domain == "" || strings.ContainsAny(domain, "/ ") {
		return nil, toolErrorf("check_domain takes a domain, like shop.com")
	}
	var sender *dns.Sender
	if provider != "" {
		if _, ok := dns.ProviderByName(provider); !ok {
			var names []string
			for _, p := range dns.Providers {
				names = append(names, p.Name)
			}
			return nil, toolErrorf("provider %q: use one of %s", provider, strings.Join(names, ", "))
		}
		sender = &dns.Sender{Provider: provider}
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	return dns.Check(ctx, domain, dns.Options{Resolver: s.DNS, Sender: sender}), nil
}
