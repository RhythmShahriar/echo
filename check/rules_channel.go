// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package check

import (
	"fmt"
	"net"
	"strings"

	"github.com/RhythmShahriar/echo/dns"
	"github.com/RhythmShahriar/echo/model"
)

// Rules of one channel: email, SMS and WhatsApp, webhooks.

var (
	emailOnly = []model.Channel{model.Email}
	phones    = []model.Channel{model.SMS, model.WhatsApp}
)

var noSubject = Rule{
	ID: "email/no-subject", Level: model.Warning, Channels: emailOnly,
	Doc: "An email has no subject.",
	Run: func(m model.Message, _ Options) []model.Issue {
		if strings.TrimSpace(m.Subject) != "" {
			return nil
		}
		return []model.Issue{{Title: "Email has no subject",
			Fix: "Give the notification a subject; many filters treat mail without one as spam."}}
	},
}

var emptyEmail = Rule{
	ID: "email/empty", Level: model.Error, Channels: emailOnly,
	Doc: "An email has neither text nor HTML.",
	Run: func(m model.Message, _ Options) []model.Issue {
		if strings.TrimSpace(m.Text) != "" || strings.TrimSpace(m.HTML) != "" {
			return nil
		}
		return []model.Issue{{Title: "Email is empty",
			Fix: "The template rendered nothing: check the notification's message setting and the variables it uses."}}
	},
}

var noText = Rule{
	ID: "email/no-text", Level: model.Notice, Channels: emailOnly,
	Doc: "An HTML email has no plain-text part.",
	Run: func(m model.Message, _ Options) []model.Issue {
		if m.HTML == "" || strings.TrimSpace(m.Text) != "" {
			return nil
		}
		return []model.Issue{{Title: "Email has no plain-text part",
			Fix: "Send a text alternative too; some clients and spam filters prefer it."}}
	},
}

var clipped = Rule{
	ID: "email/clipped", Level: model.Warning, Channels: emailOnly,
	Doc: "An email's HTML is over 102 KB, so Gmail clips it.",
	Run: func(m model.Message, _ Options) []model.Issue {
		if len(m.HTML) <= 102*1024 {
			return nil
		}
		return []model.Issue{{Title: "Email is too big for Gmail and gets clipped",
			Detail: fmt.Sprintf("%d KB of HTML; Gmail clips messages over 102 KB", len(m.HTML)/1024),
			Fix:    "Trim inline CSS and markup, or move content behind links."}}
	},
}

var fromDomain = Rule{
	ID: "email/from-domain", Level: model.Notice, Channels: emailOnly,
	Doc: "An email is sent from another registrable domain than the site's.",
	Run: func(m model.Message, o Options) []model.Issue {
		d, site := domainOf(m.From), o.domain()
		if d == "" || site == "" || isDevHost(site) || net.ParseIP(site) != nil || dns.OrgDomain(d) == dns.OrgDomain(site) {
			return nil
		}
		return []model.Issue{{Title: fmt.Sprintf("Email is sent from another domain (%s)", d),
			Detail: "the site is " + site,
			Fix:    "Send from an address on your own domain, or make sure its SPF, DKIM and DMARC cover this sender."}}
	},
}

var smsNumber = Rule{
	ID: "sms/number", Level: model.Warning, Channels: phones,
	Doc: "An SMS or WhatsApp message goes to a number that isn't in E.164 form.",
	Run: func(m model.Message, _ Options) []model.Issue {
		var out []model.Issue
		for _, to := range m.To {
			if to != "" && !e164.MatchString(to) {
				out = append(out, model.Issue{Title: "Message sent to a number that isn't in international format",
					Detail: to, Fix: "Store and send numbers in E.164 form, e.g. +4915112345678."})
			}
		}
		return out
	},
}

var smsParts = Rule{
	ID: "sms/parts", Level: model.Notice, Channels: []model.Channel{model.SMS},
	Doc: "An SMS needs more than one part (a warning over three); each part is charged.",
	Run: func(m model.Message, _ Options) []model.Issue {
		if m.Text == "" {
			return nil
		}
		enc, parts := segments(m.Text)
		detail := fmt.Sprintf("%d parts (%s); each part is charged", parts, enc)
		switch {
		case parts > 3:
			return []model.Issue{{Level: model.Warning, Title: "SMS needs more than 3 parts", Detail: detail,
				Fix: "Shorten the message, or avoid characters that force UCS-2 (emoji, curly quotes)."}}
		case parts > 1:
			return []model.Issue{{Title: "SMS needs more than one part", Detail: detail,
				Fix: "Keep SMS under 160 GSM-7 characters (70 with emoji or other Unicode)."}}
		}
		return nil
	},
}

var webhookJSON = Rule{
	ID: "webhook/json", Level: model.Warning, Channels: []model.Channel{model.Webhook},
	Doc: "A webhook says it sends JSON but its body doesn't parse.",
	Run: func(m model.Message, _ Options) []model.Issue {
		if !isJSON(m) || validJSON(m.Body) || strings.HasSuffix(m.Body, "…(truncated)") {
			return nil
		}
		return []model.Issue{{Title: fmt.Sprintf("%s sends invalid JSON", Capitalize(Describe(m))),
			Fix: "The payload says it's JSON but doesn't parse; check how it's built (json_encode errors, stray output)."}}
	},
}

var webhookSignature = Rule{
	ID: "webhook/signature", Level: model.Warning, Channels: []model.Channel{model.Webhook},
	Doc: "A WooCommerce webhook is sent without a signature.",
	Run: func(m model.Message, _ Options) []model.Issue {
		if m.Provider != "woocommerce" || m.Headers["x-wc-webhook-signature"] != "" {
			return nil
		}
		return []model.Issue{{Title: "WooCommerce webhook is sent without a signature",
			Fix: "Set a secret on the webhook (WooCommerce → Settings → Advanced → Webhooks) so receivers can verify it."}}
	},
}
