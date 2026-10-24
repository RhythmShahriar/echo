// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package recognize

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/model"
)

var chatCases = []tcase{
	{name: "MessageCard webhook", provider: "teams", status: 200,
		req: jsonReq("POST", "https://contoso.webhook.office.com/webhookb2/1111/IncomingWebhook/2222/3333", `{"@type":"MessageCard","summary":"New order","title":"Order #1042","text":"Ann paid $40"}`),
		check: func(t *testing.T, m model.Message) {
			eq(t, "channel", m.Channel, model.Chat)
			eq(t, "subject", m.Subject, "Order #1042")
			eq(t, "text", m.Text, "Ann paid $40")
		}},
	{name: "Workflows adaptive card", provider: "teams", status: 202,
		req: jsonReq("POST", "https://prod-12.westus.logic.azure.com/workflows/abc/triggers/manual/paths/invoke?api-version=2016-06-01", `{"type":"message","attachments":[{"contentType":"application/vnd.microsoft.card.adaptive",
			"content":{"type":"AdaptiveCard","body":[{"type":"TextBlock","text":"Disk almost full"}]}}]}`),
		check: func(t *testing.T, m model.Message) { eq(t, "text", m.Text, "Disk almost full") }},
}

var chatNegatives = []negative{
	{"Outlook mail API", "teams", jsonReq("GET", "https://outlook.office.com/api/v2.0/me/messages", "")},
	{"Logic app without trigger", "teams", jsonReq("GET", "https://prod-12.westus.logic.azure.com/status", "")},
}

// tgToken is a Telegram bot token, assembled at run time like join's
// other fixtures.
var tgToken = join("123456789:", "AAHZq9TOPSECRET", "value77abcdefghijkl")

func init() {
	chatCases = append(chatCases,
		tcase{name: "sendMessage JSON", provider: "telegram", status: 200,
			req: jsonReq("POST", "https://api.telegram.org/bot"+tgToken+"/sendMessage", `{"chat_id":-1001234567890,"text":"New order #1042"}`),
			check: func(t *testing.T, m model.Message) {
				eq(t, "channel", m.Channel, model.Chat)
				eq(t, "text", m.Text, "New order #1042")
				eq(t, "from", m.From, "bot 123456789")
				eqList(t, "to", m.To, "-1001234567890")
				eq(t, "event", m.Event, "")
			}},
		tcase{name: "sendPhoto form with caption", provider: "telegram", status: 200,
			req: formReq("https://api.telegram.org/bot"+tgToken+"/sendPhoto", "chat_id=%40shop_alerts&photo=https%3A%2F%2Fshop.test%2Fa.png&caption=Low+stock"),
			check: func(t *testing.T, m model.Message) {
				eq(t, "text", m.Text, "Low stock")
				eq(t, "event", m.Event, "sendPhoto")
				eqList(t, "to", m.To, "@shop_alerts")
			}},
		tcase{name: "sendMessage query string", provider: "telegram", status: 200,
			req:   req("GET", "https://api.telegram.org/bot"+tgToken+"/sendMessage?chat_id=42&text=hi", nil, ""),
			check: func(t *testing.T, m model.Message) { eq(t, "text", m.Text, "hi"); eqList(t, "to", m.To, "42") }},
	)
	chatNegatives = append(chatNegatives,
		negative{"Telegram getUpdates", "telegram", jsonReq("GET", "https://api.telegram.org/bot"+tgToken+"/getUpdates", "")},
		negative{"Telegram file download", "telegram", jsonReq("GET", "https://api.telegram.org/file/bot"+tgToken+"/photos/a.jpg", "")},
		negative{"sendMessage on another host", "telegram", jsonReq("POST", "https://chat.shop.test/bot1:x/sendMessage", `{"text":"x"}`)},
	)
}

func TestTelegramFakeIsABotAPIMessage(t *testing.T) {
	r := jsonReq("POST", "https://api.telegram.org/bot"+tgToken+"/sendMessage", `{"chat_id":42,"text":"Paid"}`)
	var m model.Message
	f := FakeFor(Recognize(r, &m), r, &m)
	var got struct {
		OK     bool `json:"ok"`
		Result struct {
			MessageID int64  `json:"message_id"`
			Text      string `json:"text"`
			Chat      struct {
				ID   int64  `json:"id"`
				Type string `json:"type"`
			} `json:"chat"`
			From struct {
				ID    int64 `json:"id"`
				IsBot bool  `json:"is_bot"`
			} `json:"from"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(f.Body), &got); err != nil {
		t.Fatal(err)
	}
	if !got.OK || got.Result.Chat.ID != 42 || got.Result.Chat.Type != "private" || got.Result.Text != "Paid" || got.Result.From.ID != 123456789 || !got.Result.From.IsBot {
		t.Errorf("fake = %s", f.Body)
	}
	if strings.Contains(f.Body, "TOPSECRET") {
		t.Error("token in the fake")
	}
}
