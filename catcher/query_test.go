// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package catcher

import (
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/model"
)

func TestParseQuery(t *testing.T) {
	order := model.Message{ID: "m1", Channel: model.Email, From: "Shop <shop@example.com>", To: []string{"buyer@example.com"},
		BCC: []string{"audit@example.com"}, Subject: "Order shipped", Text: "Thanks for your order #42.",
		Action: "checkout", Source: model.Source{Host: "shop.test", Site: "https://shop.test"},
		Attachments: []model.Attachment{{Name: "invoice.pdf"}},
		Issues:      []model.Issue{{Check: "content/dev-link"}}}
	sms := model.Message{ID: "m2", Channel: model.SMS, Provider: "twilio", To: []string{"+15551230000"}, Text: "Your code",
		Attribution: model.Late, LateFor: "login", URL: "https://api.twilio.com/2010-04-01/Messages.json"}
	tests := []struct {
		q          string
		order, sms bool
	}{
		{"", true, true},
		{"   ", true, true},
		{"to:buyer", true, false},
		{"TO:BUYER@EXAMPLE.COM", true, false},
		{"to:audit", true, false}, // Bcc counts
		{"to:+1555", false, true},
		{"from:shop@", true, false},
		{"subject:shipped", true, false},
		{`subject:"order shipped"`, true, false},
		{`subject:"order received"`, false, false},
		{"channel:email", true, false},
		{"channel:email,sms", true, true},
		{"action:checkout", true, false},
		{"action:login", false, true}, // late mail counts for its action
		{"action:check", false, false},
		{"provider:Twilio", false, true},
		{"check:content/dev-link", true, false},
		{"check:content", true, false},
		{"check:content/dev", false, false},
		{"site:shop.test", true, false},
		{"has:attachment", true, false},
		{"has:attachments", true, false},
		{"has:issue", true, false},
		{"#42", true, false},
		{`"for your order"`, true, false},
		{"thanks order", true, false},
		{"thanks sms", false, false},
		{"code", false, true},
		{"https://api.twilio.com", false, true}, // not a field: free text
		{"channel:sms code", false, true},
		{"channel:email code", false, false},
	}
	for _, tt := range tests {
		q, err := ParseQuery(tt.q)
		if err != nil {
			t.Errorf("ParseQuery(%q): %v", tt.q, err)
			continue
		}
		if got := q.Match(order); got != tt.order {
			t.Errorf("%q matches the order email: %v, want %v", tt.q, got, tt.order)
		}
		if got := q.Match(sms); got != tt.sms {
			t.Errorf("%q matches the SMS: %v, want %v", tt.q, got, tt.sms)
		}
	}
}

func TestParseQueryErrors(t *testing.T) {
	for q, want := range map[string]string{
		"to:":                "to: needs a value",
		`subject:""`:         "subject: needs a value",
		`""`:                 "empty quotes",
		"channel:fax":        "channel:fax: not a channel",
		"channel:email,":     "not a channel",
		"has:pdf":            "has takes attachment or issue",
		`subject:"order`:     "isn't closed",
		`subject:"a"b`:       "closing quote must end the word",
		`ab"c d"`:            "a quote in the middle",
		`nofield:"a b"`:      "a quote in the middle",
		`free "text`:         "isn't closed",
		"to:a has:something": "has takes",
	} {
		_, err := ParseQuery(q)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseQuery(%q) = %v, want an error with %q", q, err, want)
		}
	}
}

// TestSiteMatchesWholeLabels: site:client-a finds client-a's messages
// and never client-ab's, another site of the same hub.
func TestSiteMatchesWholeLabels(t *testing.T) {
	for _, c := range []struct {
		v    string
		src  model.Source
		want bool
	}{
		{"client-a", model.Source{Site: "client-a"}, true},
		{"CLIENT-A", model.Source{Site: "client-a"}, true},
		{"client-a", model.Source{Site: "client-ab"}, false},
		{"client-a", model.Source{Site: "https://client-a.test"}, true},
		{"client-a", model.Source{Site: "https://client-ab.test"}, false},
		{"shop.test", model.Source{Host: "staging.shop.test"}, true},
		{"shop", model.Source{Host: "shopping.test"}, false},
		{"", model.Source{Host: "shop.test"}, false},
	} {
		if got := SiteMatches(c.v, c.src); got != c.want {
			t.Errorf("site:%s on %+v = %v, want %v", c.v, c.src, got, c.want)
		}
	}
}
