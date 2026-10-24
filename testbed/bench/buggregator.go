// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/RhythmShahriar/echo/model"
)

// The testbed's Buggregator, through the gate (make up TOOLS=buggregator
// starts it).
const (
	buggregatorAPI  = "http://127.0.0.1:18857"
	buggregatorSMTP = "127.0.0.1:18858"
)

// buggregator is the Buggregator column. Buggregator is a debug hub for
// PHP: an SMTP catcher, Sentry, Ray, VarDumper, Monolog, HTTP dumps and an
// SMS module. Its SMTP inbox is judged like Mailpit's. The other modules
// only see what a site is re-pointed to send them (each SDK's URL, by
// hand), which the testbed's sites aren't, as a staging copy of a live
// site wouldn't be.
type buggregator struct{ api, smtp string }

func (buggregator) Name() string  { return "buggregator" }
func (buggregator) Title() string { return "Buggregator" }

func (buggregator) About() string {
	return "**Buggregator** (`ghcr.io/buggregator/server`, see testbed/docker-compose.yml for the version) is a debug hub for PHP with an SMTP catcher, " +
		"HTTP dumps and an SMS module. Its SMTP inbox is judged as Mailpit's is: the same emails go in over SMTP and come back through its API " +
		"(`GET /api/events?type=smtp`, cleared between states). Its HTTP dump and SMS modules only receive what a site is re-pointed to send them, " +
		"one SDK at a time, which a staging copy of a live site isn't: SMS, chat, webhooks, analytics and DNS are **can't see**."
}

// Ready reports whether Buggregator answers.
func (t buggregator) Ready(ctx context.Context) error {
	var v struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := getJSON(ctx, t.api+"/api/events?type=smtp", &v); err != nil {
		return fmt.Errorf("buggregator isn't reachable at %s (make -C testbed up TOOLS=buggregator): %w", t.api, err)
	}
	return nil
}

// Judge decides the Buggregator column for one scenario.
func (t buggregator) Judge(ctx context.Context, s Scenario, good, broken *model.Run, stamp string) Result {
	return judgeInbox(ctx, "Buggregator", t, s, good, broken, stamp)
}

// deliver clears Buggregator's SMTP events, which it can't tag, then
// sends the run's SMTP mail.
func (t buggregator) deliver(ctx context.Context, r *model.Run, tag string) (int, error) {
	if err := t.clear(ctx); err != nil {
		return 0, err
	}
	return deliverSMTP(t.smtp, "Buggregator", r, tag)
}

// clear deletes every SMTP event.
func (t buggregator) clear(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, t.api+"/api/events", bytes.NewReader([]byte(`{"type":"smtp"}`)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("clearing Buggregator: %w", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("clearing Buggregator: %s", resp.Status)
	}
	return nil
}

// bugAddress is an address as Buggregator lists it: an object with email
// and name, or a bare string in some versions.
type bugAddress struct{ Email string }

func (a *bugAddress) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		a.Email = s
		return nil
	}
	var o struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(b, &o); err != nil {
		return err
	}
	a.Email = o.Email
	return nil
}

// read returns the SMTP events Buggregator holds: all of them, since
// deliver cleared the rest.
func (t buggregator) read(ctx context.Context, _ string) ([]inboxMessage, error) {
	var out []inboxMessage
	prev := -1
	for range 10 { // it stores asynchronously: read until the count holds
		time.Sleep(300 * time.Millisecond)
		var list struct {
			Data []struct {
				Type    string `json:"type"`
				Payload struct {
					Subject string       `json:"subject"`
					From    []bugAddress `json:"from"`
					To      []bugAddress `json:"to"`
					Text    string       `json:"text"`
					HTML    string       `json:"html"`
				} `json:"payload"`
			} `json:"data"`
		}
		if err := getJSON(ctx, t.api+"/api/events?type=smtp", &list); err != nil {
			return nil, err
		}
		out = out[:0]
		for _, e := range list.Data {
			if e.Type != "" && e.Type != "smtp" {
				continue
			}
			m := inboxMessage{Subject: e.Payload.Subject, Text: e.Payload.Text, HTML: e.Payload.HTML}
			if len(e.Payload.From) > 0 {
				m.From = e.Payload.From[0].Email
			}
			for _, a := range e.Payload.To {
				m.To = append(m.To, a.Email)
			}
			out = append(out, m)
		}
		if len(out) == prev {
			break
		}
		prev = len(out)
	}
	return out, nil
}
