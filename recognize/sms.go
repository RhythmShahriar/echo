package recognize

import (
	"fmt"
	"strings"

	"github.com/RhythmShahriar/echo/model"
)

// SMS and WhatsApp providers beyond Twilio (core.go).
func init() {
	register(vonageSMS)
	register(vonageMessages)
	register(messagebird)
	register(bird)
	register(plivo)
	register(sns)
	register(telnyx)
	register(clicksend)
	register(whatsapp)
}

// vonageSMS: the legacy Nexmo SMS API, POST rest.nexmo.com/sms/json with
// form or JSON from, to, text (api_key and api_secret ride along and are
// never copied into the message).
var vonageSMS = Recognizer{
	Provider: "vonage",
	Channel:  model.SMS,
	Match: func(r Request) bool {
		return host(r, "rest.nexmo.com") && r.URL.Path == "/sms/json"
	},
	Fill: func(r Request, m *model.Message) {
		if isJSON(r) {
			var p map[string]any
			if r.JSON(&p) {
				m.To, m.From, m.Text = strs(p["to"]), str(p["from"]), str(p["text"])
			}
			return
		}
		f := values(r)
		m.To, m.From, m.Text = split(f.Get("to")), f.Get("from"), f.Get("text")
	},
	Fake: func(_ Request, m *model.Message) Fake {
		return jsonFake(200, js(map[string]any{"message-count": "1", "messages": []map[string]string{{
			"to": first(m.To), "message-id": uuid(), "status": "0", "remaining-balance": "10.00000000",
			"message-price": "0.00000000", "network": "12345"}}}))
	},
}

// vonageMessages: the Messages API, POST api.nexmo.com/v1/messages with a
// channel of sms, mms, whatsapp, viber or messenger.
var vonageMessages = Recognizer{
	Provider: "vonage",
	Channel:  model.SMS,
	Match: func(r Request) bool {
		return host(r, "api.nexmo.com", "messages-sandbox.nexmo.com") && r.URL.Path == "/v1/messages" && r.Method == "POST"
	},
	Fill: func(r Request, m *model.Message) {
		var p map[string]any
		if !r.JSON(&p) {
			return
		}
		switch str(p["channel"]) {
		case "whatsapp":
			m.Channel = model.WhatsApp
		case "messenger", "viber_service":
			m.Channel = model.Chat
		}
		m.To, m.From = strs(p["to"], "number", "id"), first(strs(p["from"], "number", "id"))
		m.Text = str(p["text"])
		if name := str(dig(p, "template", "name")); name != "" {
			m.Event = "template " + name
		}
	},
	Fake: func(Request, *model.Message) Fake {
		return jsonFake(202, js(map[string]string{"message_uuid": uuid()}))
	},
}

// messagebird: the classic API, POST rest.messagebird.com/messages with
// originator, recipients (a list or comma-separated) and body.
var messagebird = Recognizer{
	Provider: "messagebird",
	Channel:  model.SMS,
	Match: func(r Request) bool {
		return host(r, "rest.messagebird.com") && r.URL.Path == "/messages" && r.Method == "POST"
	},
	Fill: func(r Request, m *model.Message) {
		if isJSON(r) {
			var p map[string]any
			if r.JSON(&p) {
				m.To, m.From, m.Text = strs(p["recipients"]), str(p["originator"]), str(p["body"])
			}
			return
		}
		f := values(r)
		m.To, m.From, m.Text = split(f["recipients"]...), f.Get("originator"), f.Get("body")
	},
	Fake: func(_ Request, m *model.Message) Fake {
		id := randHex(32)
		items := []map[string]any{}
		for _, to := range m.To {
			items = append(items, map[string]any{"recipient": to, "status": "sent", "statusDatetime": now()})
		}
		return jsonFake(201, js(map[string]any{
			"id": id, "href": "https://rest.messagebird.com/messages/" + id, "direction": "mt", "type": "sms",
			"originator": m.From, "body": m.Text, "reference": nil, "validity": nil, "gateway": 10,
			"typeDetails": map[string]any{}, "datacoding": "plain", "mclass": 1, "scheduledDatetime": nil,
			"createdDatetime": now(),
			"recipients": map[string]any{"totalCount": len(m.To), "totalSentCount": len(m.To), "totalDeliveredCount": 0,
				"totalDeliveryFailedCount": 0, "items": items},
		}))
	},
}

// bird: the current Bird API, POST
// api.bird.com/workspaces/{ws}/channels/{channel}/messages.
var bird = Recognizer{
	Provider: "bird",
	Channel:  model.SMS,
	Match: func(r Request) bool {
		s := segments(r)
		return host(r, "api.bird.com") && len(s) == 5 && s[0] == "workspaces" && s[2] == "channels" && s[4] == "messages" && r.Method == "POST"
	},
	Fill: func(r Request, m *model.Message) {
		var p map[string]any
		if !r.JSON(&p) {
			return
		}
		m.To = strs(dig(p, "receiver", "contacts"), "identifierValue")
		m.Text = str(dig(p, "body", "text", "text"))
		if name := str(dig(p, "template", "projectId")); name != "" {
			m.Event = "template " + name
		}
	},
	Fake: func(r Request, _ *model.Message) Fake {
		return jsonFake(202, js(map[string]any{"id": uuid(), "channelId": segments(r)[3], "status": "accepted",
			"createdAt": now(), "updatedAt": now()}))
	},
}

// plivo: POST /v1/Account/{id}/Message/ with src, dst ("<"-separated for
// several) and text.
var plivo = Recognizer{
	Provider: "plivo",
	Channel:  model.SMS,
	Match: func(r Request) bool {
		s := segments(r)
		return host(r, "api.plivo.com") && len(s) == 4 && s[1] == "Account" && s[3] == "Message" && r.Method == "POST"
	},
	Fill: func(r Request, m *model.Message) {
		var p map[string]any
		if !r.JSON(&p) {
			return
		}
		m.From, m.Text = str(p["src"]), str(p["text"])
		m.To = strings.Split(str(p["dst"]), "<")
		if p["type"] == "whatsapp" {
			m.Channel = model.WhatsApp
		}
	},
	Fake: func(_ Request, m *model.Message) Fake {
		ids := make([]string, 0, len(m.To))
		for range m.To {
			ids = append(ids, uuid())
		}
		return jsonFake(202, js(map[string]any{"api_id": uuid(), "message": "message(s) queued", "message_uuid": ids}))
	},
}

// sns: Amazon SNS Publish straight to a phone number (topic publishes are
// left to the generic recognizer). The query API answers in XML; SDKs on
// the JSON protocol send X-Amz-Target AmazonSNS.Publish.
var sns = Recognizer{
	Provider: "amazon-sns",
	Channel:  model.SMS,
	Match: func(r Request) bool {
		h := strings.ToLower(r.URL.Hostname())
		if !strings.HasPrefix(h, "sns.") || !strings.HasSuffix(h, ".amazonaws.com") {
			return false
		}
		if strings.HasSuffix(r.Header("x-amz-target"), ".Publish") {
			var p map[string]any
			return r.JSON(&p) && str(p["PhoneNumber"]) != ""
		}
		f := values(r)
		return f.Get("Action") == "Publish" && f.Get("PhoneNumber") != ""
	},
	Fill: func(r Request, m *model.Message) {
		if r.Header("x-amz-target") != "" {
			var p map[string]any
			r.JSON(&p)
			m.To, m.Text = []string{str(p["PhoneNumber"])}, str(p["Message"])
			return
		}
		f := values(r)
		m.To, m.Text = []string{f.Get("PhoneNumber")}, f.Get("Message")
	},
	Fake: func(r Request, _ *model.Message) Fake {
		if r.Header("x-amz-target") != "" {
			f := jsonFake(200, js(map[string]string{"MessageId": uuid()}))
			f.Headers["content-type"] = "application/x-amz-json-1.0"
			return f
		}
		return xmlFake(fmt.Sprintf(`<PublishResponse xmlns="http://sns.amazonaws.com/doc/2010-03-31/"><PublishResult><MessageId>%s</MessageId></PublishResult><ResponseMetadata><RequestId>%s</RequestId></ResponseMetadata></PublishResponse>`,
			uuid(), uuid()))
	},
}

// telnyx: POST api.telnyx.com/v2/messages with from, to and text.
var telnyx = Recognizer{
	Provider: "telnyx",
	Channel:  model.SMS,
	Match: func(r Request) bool {
		return host(r, "api.telnyx.com") && strings.HasPrefix(r.URL.Path, "/v2/messages") && r.Method == "POST"
	},
	Fill: func(r Request, m *model.Message) {
		var p map[string]any
		if !r.JSON(&p) {
			return
		}
		m.To, m.From, m.Text = strs(p["to"]), str(p["from"]), str(p["text"])
		if m.From == "" {
			m.From = str(p["messaging_profile_id"])
		}
	},
	Fake: func(_ Request, m *model.Message) Fake {
		to := []map[string]string{}
		for _, n := range m.To {
			to = append(to, map[string]string{"phone_number": n, "status": "queued"})
		}
		return jsonFake(200, js(map[string]any{"data": map[string]any{
			"record_type": "message", "id": uuid(), "direction": "outbound", "type": "SMS",
			"from": map[string]string{"phone_number": m.From}, "to": to, "text": m.Text, "parts": 1,
			"encoding": "GSM-7", "received_at": now(), "errors": []any{}}}))
	},
}

// clicksend: POST rest.clicksend.com/v3/sms/send with a list of messages.
var clicksend = Recognizer{
	Provider: "clicksend",
	Channel:  model.SMS,
	Match: func(r Request) bool {
		return host(r, "rest.clicksend.com") && r.URL.Path == "/v3/sms/send"
	},
	Fill: func(r Request, m *model.Message) {
		var p struct {
			Messages []struct {
				To   string `json:"to"`
				From string `json:"from"`
				Body string `json:"body"`
			} `json:"messages"`
		}
		if !r.JSON(&p) {
			return
		}
		for i, msg := range p.Messages {
			m.To = append(m.To, msg.To)
			if i == 0 {
				m.From, m.Text = msg.From, msg.Body
			}
		}
	},
	Fake: func(_ Request, m *model.Message) Fake {
		msgs := []map[string]any{}
		for _, to := range m.To {
			msgs = append(msgs, map[string]any{"to": to, "from": m.From, "body": m.Text, "message_id": uuid(),
				"status": "SUCCESS", "message_parts": 1, "message_price": "0.0000", "date": 1761264000})
		}
		return jsonFake(200, js(map[string]any{"http_code": 200, "response_code": "SUCCESS",
			"response_msg": "Messages queued for delivery.", "data": map[string]any{
				"total_price": 0, "total_count": len(msgs), "queued_count": len(msgs), "messages": msgs,
				"_currency": map[string]string{"currency_name_short": "USD", "currency_prefix_d": "$"}}}))
	},
}

// whatsapp: the Meta WhatsApp Cloud API, POST
// graph.facebook.com/{version}/{phone-number-id}/messages.
var whatsapp = Recognizer{
	Provider: "whatsapp-cloud",
	Channel:  model.WhatsApp,
	Match: func(r Request) bool {
		return host(r, "graph.facebook.com") && r.Method == "POST" && graphPath(r, "messages")
	},
	Fill: func(r Request, m *model.Message) {
		var p map[string]any
		if !r.JSON(&p) {
			return
		}
		m.From = graphID(r)
		m.To = strs(p["to"])
		m.Text = str(dig(p, "text", "body"))
		if m.Text == "" {
			m.Text = str(dig(p, "interactive", "body", "text"))
		}
		if name := str(dig(p, "template", "name")); name != "" {
			m.Event = "template " + name
		}
	},
	Fake: func(_ Request, m *model.Message) Fake {
		return jsonFake(200, js(map[string]any{"messaging_product": "whatsapp",
			"contacts": []map[string]string{{"input": first(m.To), "wa_id": strings.TrimPrefix(first(m.To), "+")}},
			"messages": []map[string]string{{"id": "wamid.HBgL" + strings.ToUpper(randHex(40))}}}))
	},
}
