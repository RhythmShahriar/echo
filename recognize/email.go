// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package recognize

import (
	"fmt"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/model"
)

// Email API providers: WordPress mailer plugins (WP Mail SMTP, FluentSMTP,
// Post SMTP) and framework SDKs call these over HTTP instead of SMTP, so
// the connector sees a JSON or form request, not a wp_mail call.
func init() {
	register(sendgrid)
	register(mailgun)
	register(postmark)
	register(resend)
	register(ses)
	register(brevo)
	register(sparkpost)
	register(mandrill)
}

// addr is the {"email", "name"} object most JSON mail APIs use.
type addr struct {
	Email string `json:"email"`
	Name  string `json:"name"`
}

func emails(as []addr) []string {
	out := make([]string, 0, len(as))
	for _, a := range as {
		if a.Email != "" {
			out = append(out, a.Email)
		}
	}
	return out
}

// sendgrid: POST /v3/mail/send with personalizations (recipients per
// copy), from, subject and content parts. It answers 202 with no body.
var sendgrid = Recognizer{
	Provider: "sendgrid",
	Name:     "SendGrid",
	Channel:  model.Email,
	Hosts:    []string{"api.sendgrid.com"},
	Match: func(r Request) bool {
		return host(r, "api.sendgrid.com") && r.URL.Path == "/v3/mail/send"
	},
	Fill: func(r Request, m *model.Message) {
		var p struct {
			Personalizations []struct {
				To      []addr `json:"to"`
				CC      []addr `json:"cc"`
				BCC     []addr `json:"bcc"`
				Subject string `json:"subject"`
			} `json:"personalizations"`
			From     addr   `json:"from"`
			ReplyTo  addr   `json:"reply_to"`
			Subject  string `json:"subject"`
			Template string `json:"template_id"`
			Content  []struct {
				Type  string `json:"type"`
				Value string `json:"value"`
			} `json:"content"`
		}
		if !r.JSON(&p) {
			return
		}
		m.From, m.ReplyTo, m.Subject = p.From.Email, p.ReplyTo.Email, p.Subject
		for _, per := range p.Personalizations {
			m.To = append(m.To, emails(per.To)...)
			m.CC = append(m.CC, emails(per.CC)...)
			m.BCC = append(m.BCC, emails(per.BCC)...)
			if m.Subject == "" {
				m.Subject = per.Subject
			}
		}
		for _, c := range p.Content {
			switch c.Type {
			case "text/plain":
				m.Text = c.Value
			case "text/html":
				m.HTML = c.Value
			}
		}
		if p.Template != "" {
			m.Event = "template " + p.Template
		}
	},
	Fake: func(Request, *model.Message) Fake {
		return Fake{Status: 202, Headers: map[string]string{"x-message-id": randHex(22)}}
	},
}

// mailgun: POST /v3/{domain}/messages, form-encoded or multipart (the PHP
// SDK), with repeated to/cc/bcc fields.
var mailgun = Recognizer{
	Provider: "mailgun",
	Name:     "Mailgun",
	Channel:  model.Email,
	Hosts:    []string{"api.mailgun.net", "api.eu.mailgun.net"},
	Match: func(r Request) bool {
		s := segments(r)
		return host(r, "api.mailgun.net", "api.eu.mailgun.net") && len(s) == 3 && s[0] == "v3" &&
			(s[2] == "messages" || s[2] == "messages.mime")
	},
	Fill: func(r Request, m *model.Message) {
		f := values(r)
		m.From = f.Get("from")
		m.To, m.CC, m.BCC = split(f["to"]...), split(f["cc"]...), split(f["bcc"]...)
		m.Subject, m.Text, m.HTML = f.Get("subject"), f.Get("text"), f.Get("html")
		m.ReplyTo = f.Get("h:Reply-To")
		if t := f.Get("template"); t != "" {
			m.Event = "template " + t
		}
	},
	Fake: func(r Request, _ *model.Message) Fake {
		domain := segments(r)[1]
		return jsonFake(200, js(map[string]string{
			"id":      fmt.Sprintf("<%s.%s@%s>", time.Now().UTC().Format("20060102150405"), randHex(16), domain),
			"message": "Queued. Thank you.",
		}))
	},
}

// postmark: POST /email (or /email/withTemplate) with one message, or
// /email/batch with a list. Addresses are comma-separated strings.
type postmarkMsg struct {
	From     string `json:"From"`
	To       string `json:"To"`
	Cc       string `json:"Cc"`
	Bcc      string `json:"Bcc"`
	ReplyTo  string `json:"ReplyTo"`
	Subject  string `json:"Subject"`
	HTML     string `json:"HtmlBody"`
	Text     string `json:"TextBody"`
	Template string `json:"TemplateAlias"`
	Stream   string `json:"MessageStream"`
}

var postmark = Recognizer{
	Provider: "postmark",
	Name:     "Postmark",
	Channel:  model.Email,
	Hosts:    []string{"api.postmarkapp.com"},
	Match: func(r Request) bool {
		return host(r, "api.postmarkapp.com") && strings.HasPrefix(r.URL.Path, "/email")
	},
	Fill: func(r Request, m *model.Message) {
		var batch []postmarkMsg
		if !r.JSON(&batch) {
			var one postmarkMsg
			if !r.JSON(&one) {
				return
			}
			batch = []postmarkMsg{one}
		}
		for i, p := range batch {
			m.To = append(m.To, split(p.To)...)
			m.CC = append(m.CC, split(p.Cc)...)
			m.BCC = append(m.BCC, split(p.Bcc)...)
			if i == 0 {
				m.From, m.ReplyTo, m.Subject, m.Text, m.HTML = p.From, p.ReplyTo, p.Subject, p.Text, p.HTML
				if p.Template != "" {
					m.Event = "template " + p.Template
				}
			}
		}
	},
	Fake: func(r Request, m *model.Message) Fake {
		one := func(to string) map[string]any {
			return map[string]any{"To": to, "SubmittedAt": now(), "MessageID": uuid(), "ErrorCode": 0, "Message": "OK"}
		}
		if !strings.HasSuffix(r.URL.Path, "/batch") {
			return jsonFake(200, js(one(strings.Join(m.To, ", "))))
		}
		var batch []postmarkMsg
		r.JSON(&batch)
		out := make([]map[string]any, 0, len(batch))
		for _, p := range batch {
			out = append(out, one(p.To))
		}
		return jsonFake(200, js(out))
	},
}

// resend: POST /emails (or /emails/batch). to may be a string or a list.
type resendMsg struct {
	From    string `json:"from"`
	To      any    `json:"to"`
	CC      any    `json:"cc"`
	BCC     any    `json:"bcc"`
	ReplyTo any    `json:"reply_to"`
	Subject string `json:"subject"`
	HTML    string `json:"html"`
	Text    string `json:"text"`
}

var resend = Recognizer{
	Provider: "resend",
	Name:     "Resend",
	Channel:  model.Email,
	Hosts:    []string{"api.resend.com"},
	Match: func(r Request) bool {
		return host(r, "api.resend.com") && (r.URL.Path == "/emails" || r.URL.Path == "/emails/batch") && r.Method == "POST"
	},
	Fill: func(r Request, m *model.Message) {
		var batch []resendMsg
		if !r.JSON(&batch) {
			var one resendMsg
			if !r.JSON(&one) {
				return
			}
			batch = []resendMsg{one}
		}
		for i, p := range batch {
			m.To = append(m.To, strs(p.To)...)
			m.CC = append(m.CC, strs(p.CC)...)
			m.BCC = append(m.BCC, strs(p.BCC)...)
			if i == 0 {
				m.From, m.Subject, m.Text, m.HTML = p.From, p.Subject, p.Text, p.HTML
				m.ReplyTo = first(strs(p.ReplyTo))
			}
		}
	},
	Fake: func(r Request, _ *model.Message) Fake {
		if r.URL.Path == "/emails/batch" {
			var batch []any
			r.JSON(&batch)
			data := make([]map[string]string, 0, len(batch))
			for range batch {
				data = append(data, map[string]string{"id": uuid()})
			}
			return jsonFake(200, js(map[string]any{"data": data}))
		}
		return jsonFake(200, js(map[string]string{"id": uuid()}))
	},
}

// ses: Amazon SES. v2 is JSON (POST /v2/email/outbound-emails); v1 is the
// form-encoded query API (Action=SendEmail on /), answered in XML.
var ses = Recognizer{
	Provider: "amazon-ses",
	Name:     "Amazon SES",
	Channel:  model.Email,
	Hosts:    []string{"email.*.amazonaws.com", "email-fips.*.amazonaws.com"},
	Match: func(r Request) bool {
		h := strings.ToLower(r.URL.Hostname())
		if !strings.HasSuffix(h, ".amazonaws.com") || !strings.HasPrefix(h, "email.") && !strings.HasPrefix(h, "email-fips.") {
			return false
		}
		if r.URL.Path == "/v2/email/outbound-emails" {
			return true
		}
		a := values(r).Get("Action")
		return a == "SendEmail" || a == "SendRawEmail" || a == "SendTemplatedEmail"
	},
	Fill: func(r Request, m *model.Message) {
		if r.URL.Path == "/v2/email/outbound-emails" {
			var p struct {
				From string `json:"FromEmailAddress"`
				Dest struct {
					To  []string `json:"ToAddresses"`
					CC  []string `json:"CcAddresses"`
					BCC []string `json:"BccAddresses"`
				} `json:"Destination"`
				ReplyTo []string `json:"ReplyToAddresses"`
				Content struct {
					Simple struct {
						Subject struct{ Data string } `json:"Subject"`
						Body    struct {
							Text struct{ Data string } `json:"Text"`
							HTML struct{ Data string } `json:"Html"`
						} `json:"Body"`
					} `json:"Simple"`
					Template struct {
						Name string `json:"TemplateName"`
					} `json:"Template"`
				} `json:"Content"`
			}
			if !r.JSON(&p) {
				return
			}
			s := p.Content.Simple
			m.From, m.To, m.CC, m.BCC = p.From, p.Dest.To, p.Dest.CC, p.Dest.BCC
			m.ReplyTo = first(p.ReplyTo)
			m.Subject, m.Text, m.HTML = s.Subject.Data, s.Body.Text.Data, s.Body.HTML.Data
			if p.Content.Template.Name != "" {
				m.Event = "template " + p.Content.Template.Name
			}
			return
		}
		f := values(r)
		list := func(key string) []string {
			var out []string
			for i := 1; f.Get(fmt.Sprintf("%s.member.%d", key, i)) != ""; i++ {
				out = append(out, f.Get(fmt.Sprintf("%s.member.%d", key, i)))
			}
			return out
		}
		m.From = f.Get("Source")
		m.To = list("Destination.ToAddresses")
		m.CC = list("Destination.CcAddresses")
		m.BCC = list("Destination.BccAddresses")
		m.ReplyTo = first(list("ReplyToAddresses"))
		m.Subject = f.Get("Message.Subject.Data")
		m.Text, m.HTML = f.Get("Message.Body.Text.Data"), f.Get("Message.Body.Html.Data")
		if t := f.Get("Template"); t != "" {
			m.Event = "template " + t
		}
	},
	Fake: func(r Request, _ *model.Message) Fake {
		id := "0100" + randHex(12) + "-" + uuid() + "-000000"
		if r.URL.Path == "/v2/email/outbound-emails" {
			return jsonFake(200, js(map[string]string{"MessageId": id}))
		}
		a := values(r).Get("Action")
		return xmlFake(fmt.Sprintf(`<%[1]sResponse xmlns="http://ses.amazonaws.com/doc/2010-12-01/"><%[1]sResult><MessageId>%[2]s</MessageId></%[1]sResult><ResponseMetadata><RequestId>%[3]s</RequestId></ResponseMetadata></%[1]sResponse>`,
			a, id, uuid()))
	},
}

// brevo (formerly Sendinblue): POST /v3/smtp/email.
var brevo = Recognizer{
	Provider: "brevo",
	Name:     "Brevo",
	Channel:  model.Email,
	Hosts:    []string{"api.brevo.com", "api.sendinblue.com"},
	Match: func(r Request) bool {
		return host(r, "api.brevo.com", "api.sendinblue.com") && r.URL.Path == "/v3/smtp/email"
	},
	Fill: func(r Request, m *model.Message) {
		var p struct {
			Sender   addr   `json:"sender"`
			To       []addr `json:"to"`
			CC       []addr `json:"cc"`
			BCC      []addr `json:"bcc"`
			ReplyTo  addr   `json:"replyTo"`
			Subject  string `json:"subject"`
			HTML     string `json:"htmlContent"`
			Text     string `json:"textContent"`
			Template any    `json:"templateId"`
		}
		if !r.JSON(&p) {
			return
		}
		m.From, m.To, m.CC, m.BCC, m.ReplyTo = p.Sender.Email, emails(p.To), emails(p.CC), emails(p.BCC), p.ReplyTo.Email
		m.Subject, m.HTML, m.Text = p.Subject, p.HTML, p.Text
		if t := str(p.Template); t != "" {
			m.Event = "template " + t
		}
	},
	Fake: func(Request, *model.Message) Fake {
		return jsonFake(201, js(map[string]string{"messageId": "<" + randHex(24) + "@smtp-relay.mailin.fr>"}))
	},
}

// sparkpost: POST /api/v1/transmissions with recipients and content.
var sparkpost = Recognizer{
	Provider: "sparkpost",
	Name:     "SparkPost",
	Channel:  model.Email,
	Hosts:    []string{"api.sparkpost.com", "api.eu.sparkpost.com"},
	Match: func(r Request) bool {
		return host(r, "api.sparkpost.com", "api.eu.sparkpost.com") && strings.HasPrefix(r.URL.Path, "/api/v1/transmissions") && r.Method == "POST"
	},
	Fill: func(r Request, m *model.Message) {
		var p struct {
			Recipients []map[string]any `json:"recipients"`
			Content    map[string]any   `json:"content"`
		}
		if !r.JSON(&p) {
			return
		}
		for _, rc := range p.Recipients {
			m.To = append(m.To, strs(rc["address"], "email")...)
		}
		c := p.Content
		m.From = first(strs(c["from"], "email"))
		m.ReplyTo, m.Subject, m.Text, m.HTML = str(c["reply_to"]), str(c["subject"]), str(c["text"]), str(c["html"])
		if t := str(c["template_id"]); t != "" {
			m.Event = "template " + t
		}
	},
	Fake: func(_ Request, m *model.Message) Fake {
		return jsonFake(200, js(map[string]any{"results": map[string]any{
			"total_rejected_recipients": 0, "total_accepted_recipients": max(len(m.To), 1), "id": randHex(18)}}))
	},
}

// mandrill: Mailchimp Transactional, POST /api/1.0/messages/send(.json)
// or send-template; recipients carry a type of to, cc or bcc. The API key
// travels in the body as "key" and is never copied into the message.
var mandrill = Recognizer{
	Provider: "mandrill",
	Name:     "Mandrill",
	Channel:  model.Email,
	Hosts:    []string{"mandrillapp.com"},
	Match: func(r Request) bool {
		return host(r, "mandrillapp.com") && strings.HasPrefix(r.URL.Path, "/api/1.0/messages/send")
	},
	Fill: func(r Request, m *model.Message) {
		var p struct {
			Template string `json:"template_name"`
			Message  struct {
				FromEmail string `json:"from_email"`
				Subject   string `json:"subject"`
				HTML      string `json:"html"`
				Text      string `json:"text"`
				To        []struct {
					Email string `json:"email"`
					Type  string `json:"type"`
				} `json:"to"`
				Headers map[string]string `json:"headers"`
			} `json:"message"`
		}
		if !r.JSON(&p) {
			return
		}
		msg := p.Message
		m.From, m.Subject, m.HTML, m.Text = msg.FromEmail, msg.Subject, msg.HTML, msg.Text
		m.ReplyTo = msg.Headers["Reply-To"]
		for _, t := range msg.To {
			switch t.Type {
			case "cc":
				m.CC = append(m.CC, t.Email)
			case "bcc":
				m.BCC = append(m.BCC, t.Email)
			default:
				m.To = append(m.To, t.Email)
			}
		}
		if p.Template != "" {
			m.Event = "template " + p.Template
		}
	},
	Fake: func(_ Request, m *model.Message) Fake {
		out := []map[string]any{}
		for _, to := range append(append(append([]string{}, m.To...), m.CC...), m.BCC...) {
			out = append(out, map[string]any{"email": to, "status": "sent", "_id": randHex(32), "reject_reason": nil, "queued_reason": nil})
		}
		return jsonFake(200, js(out))
	},
}
