// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package recognize

import (
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/model"
)

const twilioMessages = "https://api.twilio.com/2010-04-01/Accounts/AC00000000000000000000000000000000/Messages.json"

var coreCases = []tcase{
	{name: "sms", provider: "twilio", status: 201,
		req: formReq(twilioMessages, "To=%2B14155550101&From=%2B14155550100&Body=Your+code+is+1234"),
		check: func(t *testing.T, m model.Message) {
			smsIs(t, m, model.SMS, "+14155550100", "Your code is 1234", "+14155550101")
		}},
	{name: "whatsapp from a messaging service", provider: "twilio", status: 201,
		req: formReq(twilioMessages, "To=whatsapp%3A%2B447700900000&MessagingServiceSid=MG123&Body=Order+shipped"),
		check: func(t *testing.T, m model.Message) {
			smsIs(t, m, model.WhatsApp, "MG123", "Order shipped", "+447700900000")
		}},
	{name: "content template", provider: "twilio", status: 201,
		req:   formReq(twilioMessages, "To=%2B14155550101&From=%2B14155550100&ContentSid=HX123"),
		check: func(t *testing.T, m model.Message) { eq(t, "event", m.Event, "template HX123") }},
	{name: "incoming webhook", provider: "slack", status: 200,
		req: jsonReq("POST", "https://hooks.slack.com/services/T0000/B0000/"+join("XXXX", "abcd"), `{"text":"New order #1042"}`),
		check: func(t *testing.T, m model.Message) {
			eq(t, "channel", m.Channel, model.Chat)
			eq(t, "text", m.Text, "New order #1042")
			eqList(t, "to", m.To, "slack webhook")
		}},
	{name: "chat.postMessage", provider: "slack", status: 200,
		req:   jsonReq("POST", "https://slack.com/api/chat.postMessage", `{"channel":"#orders","text":"Paid"}`),
		check: func(t *testing.T, m model.Message) { eqList(t, "to", m.To, "#orders") }},
	{name: "webhook", provider: "discord", status: 204,
		req: jsonReq("POST", "https://discord.com/api/webhooks/123/"+join("abcd", "efgh"), `{"content":"Deploy done","username":"Shop bot"}`),
		check: func(t *testing.T, m model.Message) {
			eq(t, "channel", m.Channel, model.Chat)
			eq(t, "text", m.Text, "Deploy done")
			eq(t, "from", m.From, "Shop bot")
			eqList(t, "to", m.To, "discord webhook")
		}},
	{name: "order webhook", provider: "woocommerce", status: 200,
		req: jsonReq("POST", "https://erp.shop.test/hooks/orders", `{"id":1042}`, "x-wc-webhook-topic", "order.created", "x-wc-webhook-source", "https://shop.test/"),
		check: func(t *testing.T, m model.Message) {
			eq(t, "channel", m.Channel, model.Webhook)
			eq(t, "event", m.Event, "order.created")
			eqList(t, "to", m.To, "erp.shop.test/hooks/orders")
		}},
}

var coreNegatives = []negative{
	{"Twilio message list", "twilio", jsonReq("GET", twilioMessages, "")},
	{"Twilio call", "twilio", formReq("https://api.twilio.com/2010-04-01/Accounts/AC0/Calls.json", "To=%2B14155550101")},
	{"Slack users API", "slack", jsonReq("GET", "https://slack.com/api/users.list", "")},
	{"Discord channel API", "discord", jsonReq("POST", "https://discord.com/api/v10/channels/123/messages", `{"content":"x"}`)},
	{"Webhook without WooCommerce headers", "woocommerce", jsonReq("POST", "https://erp.shop.test/hooks/orders", `{"id":1042}`, "x-wc-webhook-source", "")},
}

// TestCatchHookWinsOverWooCommerce: a WooCommerce webhook pointed at a
// Zapier catch hook is Zapier's (its URL is masked), with the topic kept.
func TestCatchHookWinsOverWooCommerce(t *testing.T) {
	r := jsonReq("POST", zapierHook, `{"id":1042}`, "x-wc-webhook-topic", "order.created")
	var m model.Message
	rec := Recognize(r, &m)
	eq(t, "provider", rec.Provider, "zapier")
	eq(t, "event", m.Event, "order.created")
	if strings.Contains(strings.Join(m.To, ""), "c9xyz") {
		t.Errorf("hook secret in To: %q", m.To)
	}
}
