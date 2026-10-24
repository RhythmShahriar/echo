// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package recognize

import (
	"strings"

	"github.com/RhythmShahriar/echo/model"
)

// Chat alerts beyond Slack and Discord (core.go).
func init() {
	register(teams)
}

// teams: Microsoft Teams incoming webhooks (*.webhook.office.com, the
// retired outlook.office.com/webhook) post MessageCards; the Workflows
// replacement posts Adaptive Cards to a Power Automate / Logic Apps
// trigger URL.
var teams = Recognizer{
	Provider: "teams",
	Name:     "Teams",
	Channel:  model.Chat,
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
