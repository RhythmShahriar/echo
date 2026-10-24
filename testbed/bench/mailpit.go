// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/smtp"
	"net/url"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/model"
)

// The testbed's Mailpit, through the gate.
const (
	mailpitSMTP = "127.0.0.1:18855"
	mailpitAPI  = "http://127.0.0.1:18854"
)

// The Mailpit column answers: would a team that uses Mailpit as its
// staging mail server have seen this problem? Mailpit catches mail sent
// over SMTP and shows it in an inbox; it doesn't capture API mailers, SMS,
// webhooks or analytics and it keeps no baseline to compare with.
//
// For each state the benchmark delivers, over SMTP into the testbed's
// Mailpit, the emails Echo captured that the site would have handed to an
// SMTP server (not those sent through a provider's API), tagged with the
// scenario and state, then reads them back through Mailpit's API. The
// scenario counts as caught when the inbox itself shows the problem: the
// manifest's `mailpit` text appears in a broken-state message (its From,
// To, Subject or body) and not in the good state's. A lost message is a
// miss: the inbox has one email fewer and nothing in Mailpit says one is
// missing.

// mailpit is the Mailpit column.
type mailpit struct{ api, smtp string }

func (mailpit) Name() string  { return "mailpit" }
func (mailpit) Title() string { return "Mailpit" }

func (mailpit) About() string {
	return "**Mailpit** (`axllent/mailpit:v1.29.2`) is the usual staging mail catcher: it receives mail over SMTP and shows it in an inbox. " +
		"It has no notion of a baseline, so it can't report a change; the question here is whether a person reading the Mailpit inbox would have seen the problem. " +
		"For each state, the benchmark delivers into the testbed's Mailpit, over SMTP, the emails Echo captured that the site would have handed to an SMTP server " +
		"(mail sent through a provider's API, such as Resend's, never reaches an SMTP catcher), then reads them back through Mailpit's API. " +
		"It counts as **caught** when the broken-state inbox visibly shows the problem (the scenario's `mailpit` text is in a message's From, To, Subject or body and wasn't in the good state's). " +
		"A message that is no longer sent is a **miss**: the inbox just has one email fewer. SMS, chat, webhooks, analytics and DNS are **can't see**."
}

// Ready reports whether Mailpit answers.
func (t mailpit) Ready(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.api+"/api/v1/info", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("mailpit isn't reachable at %s (make -C testbed up): %w", t.api, err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("mailpit answered %s", resp.Status)
	}
	return nil
}

// Judge decides the Mailpit column for one scenario.
func (t mailpit) Judge(ctx context.Context, s Scenario, good, broken *model.Run, stamp string) Result {
	return judgeInbox(ctx, "Mailpit", t, s, good, broken, stamp)
}

// deliver sends the run's SMTP mail into Mailpit tagged with tag and
// returns how many it sent.
func (t mailpit) deliver(_ context.Context, r *model.Run, tag string) (int, error) {
	return deliverSMTP(t.smtp, "Mailpit", r, tag)
}

// read returns the messages Mailpit holds with tag.
func (t mailpit) read(ctx context.Context, tag string) ([]inboxMessage, error) {
	time.Sleep(300 * time.Millisecond) // Mailpit stores asynchronously
	var list struct {
		Messages []struct {
			ID string `json:"ID"`
		} `json:"messages"`
	}
	if err := getJSON(ctx, t.api+"/api/v1/search?limit=200&query="+url.QueryEscape(`tag:"`+tag+`"`), &list); err != nil {
		return nil, err
	}
	out := make([]inboxMessage, 0, len(list.Messages))
	for _, s := range list.Messages {
		var m struct {
			Subject string `json:"Subject"`
			From    struct {
				Address string `json:"Address"`
			} `json:"From"`
			To []struct {
				Address string `json:"Address"`
			} `json:"To"`
			Text string `json:"Text"`
			HTML string `json:"HTML"`
		}
		if err := getJSON(ctx, t.api+"/api/v1/message/"+url.PathEscape(s.ID), &m); err != nil {
			return nil, err
		}
		im := inboxMessage{Subject: m.Subject, From: m.From.Address, Text: m.Text, HTML: m.HTML}
		for _, to := range m.To {
			im.To = append(im.To, to.Address)
		}
		out = append(out, im)
	}
	return out, nil
}

// smtpMail is whether a captured email would have reached an SMTP server
// (Mailpit) had the site used one: mail from wp_mail, Laravel's SMTP
// mailer, Nodemailer and mail()/sendmail, not mail sent through a
// provider's HTTP API (Resend, SendGrid's v3 API…).
func smtpMail(m model.Message) bool {
	return m.Channel == model.Email && m.Provider == ""
}

// deliverSMTP sends the run's SMTP mail to addr tagged with tag and
// returns how many it sent.
func deliverSMTP(addr, tool string, r *model.Run, tag string) (int, error) {
	n := 0
	for _, a := range r.Actions {
		for _, m := range a.Messages {
			if !smtpMail(m) || len(m.To) == 0 {
				continue
			}
			from := m.From
			if from == "" {
				from = "wordpress@shop.example.com"
			}
			if err := smtp.SendMail(addr, nil, address(from), addresses(m.To), mimeOf(m, from, tag)); err != nil {
				return n, fmt.Errorf("delivering to %s: %w", tool, err)
			}
			n++
		}
	}
	return n, nil
}

func address(a string) string {
	if i := strings.LastIndex(a, "<"); i >= 0 {
		return strings.TrimSuffix(strings.TrimSpace(a[i+1:]), ">")
	}
	return strings.TrimSpace(a)
}

func addresses(as []string) []string {
	out := make([]string, 0, len(as))
	for _, a := range as {
		out = append(out, address(a))
	}
	return out
}

// mimeOf rebuilds the message as it was captured: headers, text and HTML.
func mimeOf(m model.Message, from, tag string) []byte {
	var b strings.Builder
	h := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	h("From", from)
	h("To", strings.Join(m.To, ", "))
	if len(m.CC) > 0 {
		h("Cc", strings.Join(m.CC, ", "))
	}
	h("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	h("Date", m.At.Format(time.RFC1123Z))
	h("X-Tags", tag)
	h("MIME-Version", "1.0")
	text := strings.ReplaceAll(m.Text, "\n", "\r\n")
	if m.HTML == "" {
		h("Content-Type", "text/plain; charset=utf-8")
		b.WriteString("\r\n" + text + "\r\n")
		return []byte(b.String())
	}
	const boundary = "echo-testbed-alt"
	h("Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
	b.WriteString("\r\n--" + boundary + "\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + text + "\r\n")
	b.WriteString("--" + boundary + "\r\nContent-Type: text/html; charset=utf-8\r\n\r\n" + strings.ReplaceAll(m.HTML, "\n", "\r\n") + "\r\n")
	b.WriteString("--" + boundary + "--\r\n")
	return []byte(b.String())
}

func getJSON(ctx context.Context, u string, v any) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}
