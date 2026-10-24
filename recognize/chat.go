// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package recognize

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/model"
)

// Chat alerts beyond Slack and Discord (core.go).
func init() {
	register(teams)
	register(telegram)
}

// teams: Microsoft Teams incoming webhooks (*.webhook.office.com, the
// retired outlook.office.com/webhook) post MessageCards; the Workflows
// replacement posts Adaptive Cards to a Power Automate / Logic Apps
// trigger URL.
var teams = Recognizer{
	Provider: "teams",
	Name:     "Teams",
	Channel:  model.Chat,
	Hosts:    []string{"webhook.office.com", "outlook.office.com", "outlook.office365.com", "logic.azure.com", "api.powerplatform.com"},
	Match: func(r Request) bool {
		p := r.URL.Path
		switch {
		case host(r, "webhook.office.com"):
			return true
		case host(r, "outlook.office.com", "outlook.office365.com"):
			return strings.HasPrefix(p, "/webhook")
		case host(r, "logic.azure.com", "api.powerplatform.com"):
			return strings.Contains(p, "/workflows/") && strings.Contains(p, "/triggers/")
		}
		return false
	},
	Fill: func(r Request, m *model.Message) {
		m.To = []string{"teams webhook"}
		var p map[string]any
		if !r.JSON(&p) {
			return
		}
		m.Subject = str(p["title"])
		if m.Subject == "" {
			m.Subject = str(p["summary"])
		}
		m.Text = str(p["text"])
		if m.Text != "" {
			return
		}
		// Adaptive Cards: collect the TextBlocks.
		var lines []string
		var walk func(v any)
		walk = func(v any) {
			switch t := v.(type) {
			case map[string]any:
				if t["type"] == "TextBlock" {
					lines = append(lines, str(t["text"]))
				}
				for _, c := range t {
					walk(c)
				}
			case []any:
				for _, c := range t {
					walk(c)
				}
			}
		}
		walk(p["attachments"])
		if len(lines) == 0 {
			walk(p["sections"])
		}
		m.Text = strings.Join(lines, "\n")
	},
	Fake: func(r Request, _ *model.Message) Fake {
		if host(r, "logic.azure.com", "api.powerplatform.com") {
			return Fake{Status: 202}
		}
		return textFake(200, "1")
	},
}

// telegramBot matches the first path segment of a Bot API call,
// bot<id>:<token>. The token is the bot's credential; the ID is public.
var telegramBot = regexp.MustCompile(`^bot(\d+):(.+)$`)

// telegram: the Bot API, api.telegram.org/bot<id>:<token>/send<Thing>.
// Parameters come as JSON, a form, multipart (sendPhoto with a file) or
// the query string. Only send methods are messages: getUpdates, getMe and
// setWebhook are the bot looking after itself.
var telegram = Recognizer{
	Provider: "telegram",
	Name:     "Telegram",
	Channel:  model.Chat,
	Hosts:    []string{"api.telegram.org"},
	Match: func(r Request) bool {
		s := segments(r)
		return host(r, "api.telegram.org") && len(s) == 2 && telegramBot.MatchString(s[0]) && strings.HasPrefix(s[1], "send")
	},
	Fill: func(r Request, m *model.Message) {
		s := segments(r)
		p := map[string]any{}
		if isJSON(r) {
			r.JSON(&p)
		} else {
			for k, v := range values(r) {
				p[k] = v[0]
			}
		}
		if chat := str(p["chat_id"]); chat != "" {
			m.To = []string{chat}
		}
		m.From = "bot " + telegramBot.FindStringSubmatch(s[0])[1]
		m.Text = str(p["text"])
		if m.Text == "" {
			m.Text = str(p["caption"])
		}
		if s[1] != "sendMessage" {
			m.Event = s[1]
		}
	},
	// The Bot API answers {"ok":true,"result":<Message>}; SDKs read
	// result.message_id and result.chat.id.
	Fake: func(r Request, m *model.Message) Fake {
		bot, _ := strconv.ParseInt(strings.TrimPrefix(m.From, "bot "), 10, 64)
		chat := map[string]any{"id": first(m.To), "type": "channel", "username": strings.TrimPrefix(first(m.To), "@")}
		if id, err := strconv.ParseInt(first(m.To), 10, 64); err == nil {
			chat = map[string]any{"id": id, "type": "private"}
			if id < 0 {
				chat["type"] = "group"
			}
		}
		msg := map[string]any{
			"message_id": time.Now().UnixNano() % 1_000_000,
			"from":       map[string]any{"id": bot, "is_bot": true, "first_name": "Echo"},
			"chat":       chat,
			"date":       time.Now().Unix(),
		}
		if m.Text != "" {
			key := "text"
			if m.Event != "" {
				key = "caption"
			}
			msg[key] = m.Text
		}
		return jsonFake(200, js(map[string]any{"ok": true, "result": msg}))
	},
}
