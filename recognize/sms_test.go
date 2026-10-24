// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package recognize

import (
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/model"
)

func smsIs(t *testing.T, m model.Message, ch model.Channel, from, text string, to ...string) {
	t.Helper()
	eq(t, "channel", m.Channel, ch)
	eq(t, "from", m.From, from)
	eq(t, "text", m.Text, text)
	eqList(t, "to", m.To, to...)
}

var smsCases = []tcase{
	{name: "sms json form", provider: "vonage", status: 200,
		req: formReq("https://rest.nexmo.com/sms/json", "api_key=ab12cd34&api_secret=Zx81kQpLmN0aBcDe&from=Shop&to=447700900000&text=Your+code+is+1234"),
		check: func(t *testing.T, m model.Message) {
			smsIs(t, m, model.SMS, "Shop", "Your code is 1234", "447700900000")
			if strings.Contains(strings.Join(m.To, "")+m.Text+m.From, "Zx81") {
				t.Error("api_secret copied into the message")
			}
		}},
	{name: "messages api whatsapp", provider: "vonage", status: 202,
		req: jsonReq("POST", "https://api.nexmo.com/v1/messages", `{"message_type":"text","channel":"whatsapp","to":"447700900001","from":"447700900002","text":"Order shipped"}`),
		check: func(t *testing.T, m model.Message) {
			smsIs(t, m, model.WhatsApp, "447700900002", "Order shipped", "447700900001")
		}},
	{name: "messages", provider: "messagebird", status: 201,
		req:   jsonReq("POST", "https://rest.messagebird.com/messages", `{"originator":"Shop","recipients":[31612345678],"body":"Hello"}`, "authorization", "AccessKey test_AbCdEfGh1234"),
		check: func(t *testing.T, m model.Message) { smsIs(t, m, model.SMS, "Shop", "Hello", "31612345678") }},
	{name: "channel message", provider: "bird", status: 202,
		req: jsonReq("POST", "https://api.bird.com/workspaces/ws-1/channels/ch-9/messages",
			`{"receiver":{"contacts":[{"identifierValue":"+31612345678"}]},"body":{"type":"text","text":{"text":"Your table is ready"}}}`),
		check: func(t *testing.T, m model.Message) { smsIs(t, m, model.SMS, "", "Your table is ready", "+31612345678") }},
	{name: "message", provider: "plivo", status: 202,
		req: jsonReq("POST", "https://api.plivo.com/v1/Account/MAXXXXXXXXXXXXXXXXXX/Message/", `{"src":"+14155550100","dst":"+14155550101<+14155550102","text":"Hi"}`),
		check: func(t *testing.T, m model.Message) {
			smsIs(t, m, model.SMS, "+14155550100", "Hi", "+14155550101", "+14155550102")
		}},
	{name: "publish to phone, query API", provider: "amazon-sns", status: 200,
		req:   formReq("https://sns.us-east-1.amazonaws.com/", "Action=Publish&PhoneNumber=%2B14155550100&Message=Code+1234&Version=2010-03-31"),
		check: func(t *testing.T, m model.Message) { smsIs(t, m, model.SMS, "", "Code 1234", "+14155550100") }},
	{name: "publish to phone, JSON protocol", provider: "amazon-sns", status: 200,
		req:   jsonReq("POST", "https://sns.eu-west-1.amazonaws.com/", `{"PhoneNumber":"+447700900000","Message":"Code 9"}`, "x-amz-target", "AmazonSNS.Publish"),
		check: func(t *testing.T, m model.Message) { smsIs(t, m, model.SMS, "", "Code 9", "+447700900000") }},
	{name: "messages", provider: "telnyx", status: 200,
		req:   jsonReq("POST", "https://api.telnyx.com/v2/messages", `{"from":"+14155550100","to":"+14155550101","text":"Hello"}`),
		check: func(t *testing.T, m model.Message) { smsIs(t, m, model.SMS, "+14155550100", "Hello", "+14155550101") }},
	{name: "sms send", provider: "clicksend", status: 200,
		req: jsonReq("POST", "https://rest.clicksend.com/v3/sms/send", `{"messages":[{"source":"php","from":"Shop","to":"+61411111111","body":"Ready for pickup"}]}`),
		check: func(t *testing.T, m model.Message) {
			smsIs(t, m, model.SMS, "Shop", "Ready for pickup", "+61411111111")
		}},
	{name: "text message", provider: "whatsapp-cloud", status: 200,
		req: jsonReq("POST", "https://graph.facebook.com/v21.0/106540352242922/messages", `{"messaging_product":"whatsapp","to":"447700900000","type":"text","text":{"body":"Hi Ann"}}`),
		check: func(t *testing.T, m model.Message) {
			smsIs(t, m, model.WhatsApp, "106540352242922", "Hi Ann", "447700900000")
		}},
	{name: "template message", provider: "whatsapp-cloud", status: 200,
		req:   jsonReq("POST", "https://graph.facebook.com/v21.0/106540352242922/messages", `{"messaging_product":"whatsapp","to":"447700900000","type":"template","template":{"name":"order_update","language":{"code":"en"}}}`),
		check: func(t *testing.T, m model.Message) { eq(t, "event", m.Event, "template order_update") }},
}

var smsNegatives = []negative{
	{"Vonage number search", "vonage", jsonReq("GET", "https://rest.nexmo.com/number/search", "")},
	{"MessageBird balance", "messagebird", jsonReq("GET", "https://rest.messagebird.com/balance", "")},
	{"Bird contacts", "bird", jsonReq("POST", "https://api.bird.com/workspaces/ws-1/contacts", `{}`)},
	{"Plivo calls", "plivo", jsonReq("POST", "https://api.plivo.com/v1/Account/MAXX/Call/", `{}`)},
	{"SNS topic publish", "amazon-sns", formReq("https://sns.us-east-1.amazonaws.com/", "Action=Publish&TopicArn=arn%3Aaws%3Asns%3Aus-east-1%3A1%3Aorders&Message=x")},
	{"Telnyx calls", "telnyx", jsonReq("POST", "https://api.telnyx.com/v2/calls", `{}`)},
	{"ClickSend account", "clicksend", jsonReq("GET", "https://rest.clicksend.com/v3/account", "")},
	{"Meta CAPI is not WhatsApp", "whatsapp-cloud", jsonReq("POST", "https://graph.facebook.com/v21.0/123456/events", `{"data":[]}`)},
}
