package recognize

import (
	"fmt"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/model"
)

// The core recognizers: the most common SMS provider, chat webhooks and
// WooCommerce webhooks. Other providers live in their own files.
func init() {
	register(twilio)
	register(slack)
	register(discord)
	register(wooWebhook)
}

// twilio: POST /2010-04-01/Accounts/{sid}/Messages.json, form-encoded To,
// From, Body (or ContentSid for templates). WhatsApp numbers are prefixed
// "whatsapp:".
var twilio = Recognizer{
	Provider: "twilio",
	Channel:  model.SMS,
	Match: func(r Request) bool {
		return host(r, "api.twilio.com") && strings.HasSuffix(r.URL.Path, "/Messages.json") && r.Method == "POST"
	},
	Fill: func(r Request, m *model.Message) {
		f := r.Form()
		to, from := f.Get("To"), f.Get("From")
		if strings.HasPrefix(to, "whatsapp:") {
			m.Channel = model.WhatsApp
		}
		m.To = []string{strings.TrimPrefix(to, "whatsapp:")}
		m.From = strings.TrimPrefix(from, "whatsapp:")
		if m.From == "" {
			m.From = f.Get("MessagingServiceSid")
		}
		m.Text = f.Get("Body")
		if m.Text == "" && f.Get("ContentSid") != "" {
			m.Event = "template " + f.Get("ContentSid")
		}
	},
	Fake: func(r Request, m *model.Message) Fake {
		sid := "SM" + model.NewID(fmt.Sprint(time.Now().UnixNano(), m.Text, first(m.To))) + strings.Repeat("0", 20)
		body := fmt.Sprintf(`{"sid":%q,"status":"queued","to":%q,"from":%q,"body":%q,"date_created":%q,"num_segments":"1","error_code":null,"error_message":null}`,
			sid[:34], first(m.To), m.From, m.Text, time.Now().UTC().Format(time.RFC1123Z))
		return Fake{Status: 201, Headers: map[string]string{"content-type": "application/json"}, Body: body}
	},
}

// slack: incoming webhooks post JSON {"text": …, "blocks": …}.
var slack = Recognizer{
	Provider: "slack",
	Channel:  model.Chat,
	Match: func(r Request) bool {
		return host(r, "hooks.slack.com") || (host(r, "slack.com") && strings.HasPrefix(r.URL.Path, "/api/chat.postMessage"))
	},
	Fill: func(r Request, m *model.Message) {
		var p struct {
			Text    string `json:"text"`
			Channel string `json:"channel"`
		}
		if r.JSON(&p) {
			m.Text = p.Text
			if p.Channel != "" {
				m.To = []string{p.Channel}
			}
		}
		if len(m.To) == 0 {
			m.To = []string{"slack webhook"}
		}
	},
	Fake: func(Request, *model.Message) Fake {
		return Fake{Status: 200, Headers: map[string]string{"content-type": "text/plain"}, Body: "ok"}
	},
}

// discord: webhooks post JSON {"content": …, "embeds": …}.
var discord = Recognizer{
	Provider: "discord",
	Channel:  model.Chat,
	Match: func(r Request) bool {
		return (host(r, "discord.com") || host(r, "discordapp.com")) && strings.HasPrefix(r.URL.Path, "/api/webhooks/")
	},
	Fill: func(r Request, m *model.Message) {
		var p struct {
			Content  string `json:"content"`
			Username string `json:"username"`
		}
		if r.JSON(&p) {
			m.Text, m.From = p.Content, p.Username
		}
		m.To = []string{"discord webhook"}
	},
	Fake: func(Request, *model.Message) Fake { return Fake{Status: 204} },
}

// wooWebhook: WooCommerce delivers webhooks with X-WC-Webhook-* headers.
var wooWebhook = Recognizer{
	Provider: "woocommerce",
	Channel:  model.Webhook,
	Match:    func(r Request) bool { return r.Header("x-wc-webhook-topic") != "" },
	Fill: func(r Request, m *model.Message) {
		m.Event = r.Header("x-wc-webhook-topic")
		m.To = []string{r.URL.Host + r.URL.Path}
	},
}

func first(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}
