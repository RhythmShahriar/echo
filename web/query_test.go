// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package web

import (
	"testing"

	"github.com/RhythmShahriar/echo/model"
)

func TestQuery(t *testing.T) {
	m := model.Message{ID: "abc123", Channel: model.Email, Provider: "sendgrid", From: "Shop <shop@shop.test>", To: []string{"ann@example.org"},
		CC: []string{"audit@shop.test"}, Subject: "Order shipped", Text: "Your parcel is on its way", Action: "checkout", Run: "r1",
		Status: model.Faked, Source: model.Source{Host: "staging.shop.test"}, URL: "https://api.sendgrid.com/v3/mail/send"}
	f := facts{issues: []model.Issue{{Check: "content/placeholder", Level: model.Error}}, attachments: []Attachment{{Name: "a.pdf"}}}
	bare := facts{}
	for _, c := range []struct {
		q    string
		f    facts
		want bool
	}{
		{"", bare, true},
		{"to:ann@", bare, true},
		{"to:audit", bare, true},
		{"to:bob", bare, false},
		{"from:shop.test", bare, true},
		{"from:ann", bare, false},
		{`subject:"order shipped"`, bare, true},
		{"subject:parcel", bare, false},
		{"channel:email", bare, true},
		{"channel:EMAIL", bare, true},
		{"channel:sms", bare, false},
		{"channel:emai", bare, false}, // a channel is matched whole
		{"action:checkout", bare, true},
		{"action:r1", bare, true},
		{"action:signup", bare, false},
		{"provider:sendgrid", bare, true},
		{"provider:mailgun", bare, false},
		{"check:placeholder", f, true},
		{"check:placeholder", bare, false},
		{"site:staging", bare, true},
		{"site:prod", bare, false},
		{"status:faked", bare, true},
		{"status:sent", bare, false},
		{"has:attachment", f, true},
		{"has:attachment", bare, false},
		{"has:issue", f, true},
		{"has:html", bare, false},
		{"has:nonsense", f, false},
		{"parcel", bare, true},
		{"PARCEL way", bare, true},
		{"parcel bicycle", bare, false},
		{`"on its way"`, bare, true},
		{"api.sendgrid.com", bare, true},
		{"https://api.sendgrid.com/v3", bare, true}, // "https" isn't a key: the whole word is text
		{"-channel:sms", bare, true},
		{"-channel:email", bare, false},
		{"-", bare, false}, // nothing to negate: the text "-", which no field holds
		{"to: ann", bare, true},
	} {
		if got := Parse(c.q).Match(m, c.f); got != c.want {
			t.Errorf("%q matched %v, want %v", c.q, got, c.want)
		}
	}
	if !Parse("  ").Empty() || Parse("x").Empty() {
		t.Error("Empty")
	}
}
