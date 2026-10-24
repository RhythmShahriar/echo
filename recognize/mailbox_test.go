// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package recognize

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/model"
)

// rawMail is the RFC 5322 message the Gmail and Graph MIME fixtures send.
var rawMail = strings.ReplaceAll(`From: Shop <orders@shop.test>
To: Ann <ann@example.com>, bob@example.com
Cc: cy@example.com
Bcc: audit@shop.test
Reply-To: help@shop.test
Subject: =?UTF-8?Q?Your_caf=C3=A9_order_1042?=
MIME-Version: 1.0
Content-Type: multipart/alternative; boundary=b1

--b1
Content-Type: text/plain; charset=utf-8

Thanks, Ann.
--b1
Content-Type: text/html; charset=utf-8

<p>Thanks, Ann.</p>
--b1--
`, "\n", "\r\n")

func rawMailIs(t *testing.T, m model.Message) {
	t.Helper()
	emailIs(t, m, "orders@shop.test", "Your café order 1042", "ann@example.com", "bob@example.com")
	eqList(t, "cc", m.CC, "cy@example.com")
	eqList(t, "bcc", m.BCC, "audit@shop.test")
	eq(t, "reply-to", m.ReplyTo, "help@shop.test")
	eq(t, "text", strings.TrimSpace(m.Text), "Thanks, Ann.")
	eq(t, "html", strings.TrimSpace(m.HTML), "<p>Thanks, Ann.</p>")
}

var mailboxCases = []tcase{
	{name: "sendMail JSON", provider: "microsoft-graph", status: 202,
		req: jsonReq("POST", "https://graph.microsoft.com/v1.0/users/orders@shop.test/sendMail", `{"message":{
			"subject":"Your order","body":{"contentType":"HTML","content":"<p>Thanks</p>"},
			"toRecipients":[{"emailAddress":{"address":"ann@example.com","name":"Ann"}}],
			"ccRecipients":[{"emailAddress":{"address":"cy@example.com"}}],
			"replyTo":[{"emailAddress":{"address":"help@shop.test"}}]},"saveToSentItems":false}`),
		check: func(t *testing.T, m model.Message) {
			emailIs(t, m, "orders@shop.test", "Your order", "ann@example.com")
			eqList(t, "cc", m.CC, "cy@example.com")
			eq(t, "reply-to", m.ReplyTo, "help@shop.test")
			eq(t, "html", m.HTML, "<p>Thanks</p>")
		}},
	{name: "sendMail as me, text body, explicit from", provider: "microsoft-graph", status: 202,
		req: jsonReq("POST", "https://graph.microsoft.com/v1.0/me/sendMail", `{"message":{"subject":"Hi",
			"body":{"contentType":"Text","content":"Hello"},"from":{"emailAddress":{"address":"shared@shop.test"}},
			"toRecipients":[{"emailAddress":{"address":"ann@example.com"}}]}}`),
		check: func(t *testing.T, m model.Message) {
			emailIs(t, m, "shared@shop.test", "Hi", "ann@example.com")
			eq(t, "text", m.Text, "Hello")
		}},
	{name: "sendMail MIME", provider: "microsoft-graph", status: 202,
		req: req("POST", "https://graph.microsoft.com/v1.0/users/0f1e2d3c/sendMail", map[string]string{"content-type": "text/plain"},
			base64.StdEncoding.EncodeToString([]byte(rawMail))),
		check: rawMailIs},
	{name: "send raw", provider: "gmail", status: 200,
		req:   jsonReq("POST", "https://gmail.googleapis.com/gmail/v1/users/me/messages/send", `{"raw":"`+base64.URLEncoding.EncodeToString([]byte(rawMail))+`"}`),
		check: rawMailIs},
	{name: "send raw, unpadded, older host", provider: "gmail", status: 200,
		req:   jsonReq("POST", "https://www.googleapis.com/gmail/v1/users/orders%40shop.test/messages/send", `{"raw":"`+base64.RawURLEncoding.EncodeToString([]byte(rawMail))+`"}`),
		check: rawMailIs},
	{name: "media upload", provider: "gmail", status: 200,
		req: req("POST", "https://gmail.googleapis.com/upload/gmail/v1/users/me/messages/send?uploadType=media",
			map[string]string{"content-type": "message/rfc822"}, rawMail),
		check: rawMailIs},
	{name: "multipart upload", provider: "gmail", status: 200,
		req: req("POST", "https://gmail.googleapis.com/upload/gmail/v1/users/me/messages/send?uploadType=multipart",
			map[string]string{"content-type": "multipart/related; boundary=up"},
			"--up\r\nContent-Type: application/json\r\n\r\n{}\r\n--up\r\nContent-Type: message/rfc822\r\n\r\n"+rawMail+"\r\n--up--\r\n"),
		check: rawMailIs},
}

var mailboxNegatives = []negative{
	{"Graph user lookup", "microsoft-graph", jsonReq("GET", "https://graph.microsoft.com/v1.0/users/orders@shop.test", "")},
	{"Graph draft", "microsoft-graph", jsonReq("POST", "https://graph.microsoft.com/v1.0/me/messages", `{}`)},
	{"Gmail list", "gmail", jsonReq("GET", "https://gmail.googleapis.com/gmail/v1/users/me/messages", "")},
	{"Gmail draft", "gmail", jsonReq("POST", "https://gmail.googleapis.com/gmail/v1/users/me/drafts", `{}`)},
}

func TestGmailFakeLooksSent(t *testing.T) {
	r := mailboxCases[3].req
	var m model.Message
	f := FakeFor(Recognize(r, &m), r, &m)
	if !strings.Contains(f.Body, `"labelIds":["SENT"]`) || !strings.Contains(f.Body, `"threadId"`) {
		t.Errorf("fake = %s", f.Body)
	}
}
