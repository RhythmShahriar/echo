// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package check

import (
	"fmt"
	"slices"
	"strings"

	"github.com/RhythmShahriar/echo/model"
)

// Rules on what a reader sees, and on what a payload carries.

var sendFailed = Rule{
	ID: "send/failed", Level: model.Error,
	Doc: "The site tried to send the message and the send failed.",
	Run: func(m model.Message, _ Options) []model.Issue {
		if m.Status != model.Failed {
			return nil
		}
		what := Capitalize(Describe(m))
		title := what + " fails"
		if m.Response != nil && m.Response.Status > 0 {
			title = fmt.Sprintf("%s fails (HTTP %d)", what, m.Response.Status)
		}
		return []model.Issue{{Title: title, Detail: firstNonEmpty(m.Error, responseText(m)),
			Fix: "Check the sender's settings (API key, SMTP login, endpoint URL) and the provider's error above."}}
	},
}

var placeholder = Rule{
	ID: "content/placeholder", Level: model.Error, Channels: readable,
	Doc: "A merge tag or placeholder ({first_name}, {{ name }}, *|FNAME|*) was never filled in.",
	Run: func(m model.Message, _ Options) []model.Issue {
		var out []model.Issue
		for _, tag := range placeholders(m.Subject + "\n" + visibleText(m)) {
			out = append(out, model.Issue{Title: fmt.Sprintf("Unfilled placeholder %s in %s", tag, Describe(m)),
				Detail: "The template still contains the placeholder, so readers see it instead of their data.",
				Fix:    "Check the tag's spelling against the plugin's list of tags, and that the data it needs exists when the message is sent."})
		}
		return out
	},
}

var phpErrorRule = Rule{
	ID: "content/php-error", Level: model.Error, Channels: readable,
	Doc: "A PHP error, warning or notice was printed into the message.",
	Run: func(m model.Message, _ Options) []model.Issue {
		e := phpError(visibleText(m))
		if e == "" {
			return nil
		}
		return []model.Issue{{Title: fmt.Sprintf("PHP error printed in %s", Describe(m)), Detail: e,
			Fix: "Fix the code the error points at, and turn display_errors off so errors never reach messages."}}
	},
}

var encoding = Rule{
	ID: "content/encoding", Level: model.Warning, Channels: readable,
	Doc: "Garbled characters (â€™, Ã©) from text that was encoded twice.",
	Run: func(m model.Message, _ Options) []model.Issue {
		bad := mojibake(m.Subject + visibleText(m))
		if bad == "" {
			return nil
		}
		return []model.Issue{{Title: fmt.Sprintf("Garbled characters in %s", Describe(m)),
			Detail: fmt.Sprintf("found %q: text was encoded twice", bad),
			Fix:    "Make sure the message is built and sent as UTF-8 (the mailer's charset and the template file's encoding)."}}
	},
}

var devLink = Rule{
	ID: "content/dev-link", Level: model.Warning, Channels: readable,
	Doc: "A link points at a development address (localhost, *.test, a private IP) while the site isn't one.",
	Run: func(m model.Message, o Options) []model.Issue {
		var out []model.Issue
		for _, h := range devLinks(m, o) {
			out = append(out, model.Issue{Title: fmt.Sprintf("%s links to a development address (%s)", Capitalize(Describe(m)), h),
				Fix: "Build links from the site's configured URL, not a hard-coded or local one."})
		}
		return out
	},
}

var secretInBody = Rule{
	ID: "content/secret", Level: model.Warning,
	Channels: []model.Channel{model.SMS, model.WhatsApp, model.Chat, model.Webhook, model.Analytics, model.Payment, model.Push, model.HTTP},
	Doc:      "A payload carries a credential (Echo masked it).",
	// Whatever happened to the message: a sealed site's payload says what
	// the live site would send.
	Run: func(m model.Message, _ Options) []model.Issue {
		if !strings.Contains(m.Body, "•••••") {
			return nil
		}
		return []model.Issue{{Title: fmt.Sprintf("%s carries a credential in its body", Capitalize(Describe(m))),
			Fix: "Don't put API keys or passwords in payloads; send them in an Authorization header, or not at all."}}
	},
}

var realRecipientRule = Rule{
	ID: "seal/real-recipient", Level: model.Warning,
	Doc: "A message really went out (observe mode) to an address or number outside allow_recipients.",
	Run: func(m model.Message, o Options) []model.Issue {
		if m.Status != model.Sent {
			return nil
		}
		for _, r := range append(append(slices.Clone(m.To), m.CC...), m.BCC...) {
			if realRecipient(r) && !o.allowed(r) {
				return []model.Issue{{Title: fmt.Sprintf("%s really sent to an address outside the allowlist", Capitalize(Describe(m))),
					Detail: r, Fix: "Run sealed on staging (rhythm echo up), or add test recipients to allow_recipients."}}
			}
		}
		return nil
	},
}
