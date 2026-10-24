// Package recognize turns an outbound HTTP request into a message: it
// works out the channel (SMS, chat, webhook…), the provider and the
// recipients and content, and says what a sealed staging site should be
// answered with. Connectors only report raw requests; everything
// provider-specific lives here, with a fixture test per recognizer.
package recognize

import (
	"encoding/json"
	"net/url"
	"slices"
	"strings"

	"github.com/RhythmShahriar/echo/model"
)

// Request is an outbound HTTP request as a connector or the proxy saw it.
type Request struct {
	Method  string
	URL     *url.URL
	Headers map[string]string // lowercased names
	Body    []byte
}

// Header returns a header value (names are case-insensitive).
func (r Request) Header(name string) string {
	return r.Headers[strings.ToLower(name)]
}

// JSON decodes the body into v; it reports whether that worked.
func (r Request) JSON(v any) bool {
	return json.Unmarshal(r.Body, v) == nil
}

// Form decodes a form-encoded body (or the query string when the body is
// empty).
func (r Request) Form() url.Values {
	if len(r.Body) == 0 {
		return r.URL.Query()
	}
	v, err := url.ParseQuery(string(r.Body))
	if err != nil {
		return url.Values{}
	}
	return v
}

// Fake is the response Echo gives instead of the real service when a site
// is sealed. It looks enough like the provider's own answer that the
// site's code carries on normally (WooCommerce disables a webhook after
// five failures, SDKs throw on unexpected bodies).
type Fake struct {
	Status  int
	Headers map[string]string
	Body    string
}

// Recognizer understands one provider's API.
type Recognizer struct {
	Provider string
	Channel  model.Channel
	// Match reports whether the request is for this provider.
	Match func(r Request) bool
	// Fill sets recipients, content and event on m from the request.
	Fill func(r Request, m *model.Message)
	// Fake is the sealed answer; nil gives a generic 200 {"ok":true}.
	Fake func(r Request, m *model.Message) Fake
	// Refuse, when set, reports that r must never reach the provider nor
	// get a success-shaped answer: a live-mode payment call. It applies in
	// every mode, even to allowlisted hosts. Fake then answers with the
	// provider's own error, and the message is counted as blocked.
	Refuse func(r Request) bool
}

// registry holds every recognizer, most specific first. Recognizers are
// added from init functions in this package's provider files.
var registry []Recognizer

func register(r Recognizer) { registry = append(registry, r) }

// Recognize fills m from r: channel, provider, recipients, content. It
// returns the matching recognizer, or the generic one.
func Recognize(r Request, m *model.Message) Recognizer {
	for _, rec := range registry {
		if rec.Match(r) {
			m.Channel, m.Provider = rec.Channel, rec.Provider
			if rec.Fill != nil {
				rec.Fill(r, m)
			}
			return rec
		}
	}
	m.Channel, m.Provider = generic.Channel, generic.Provider
	generic.Fill(r, m)
	return generic
}

// FakeFor returns the sealed answer for a recognized request.
func FakeFor(rec Recognizer, r Request, m *model.Message) Fake {
	if rec.Fake != nil {
		return rec.Fake(r, m)
	}
	return Fake{Status: 200, Headers: map[string]string{"content-type": "application/json"}, Body: `{"ok":true}`}
}

// Providers lists the providers Echo knows, each once (a provider may have
// several recognizers, one per API), for docs and `rhythm echo doctor`.
func Providers() []string {
	out := make([]string, 0, len(registry))
	for _, r := range registry {
		if !slices.Contains(out, r.Provider) {
			out = append(out, r.Provider)
		}
	}
	return out
}

// names are the providers' display names, for titles and reports.
var names = map[string]string{
	"twilio": "Twilio", "slack": "Slack", "discord": "Discord", "woocommerce": "WooCommerce", "teams": "Teams",
	"sendgrid": "SendGrid", "mailgun": "Mailgun", "postmark": "Postmark", "resend": "Resend", "amazon-ses": "Amazon SES",
	"brevo": "Brevo", "sparkpost": "SparkPost", "mandrill": "Mandrill",
	"vonage": "Vonage", "messagebird": "MessageBird", "bird": "Bird", "plivo": "Plivo", "amazon-sns": "Amazon SNS",
	"telnyx": "Telnyx", "clicksend": "ClickSend", "whatsapp-cloud": "WhatsApp Cloud API",
	"ga4": "GA4", "meta-capi": "Meta Conversions API", "stripe": "Stripe", "paypal": "PayPal", "fcm": "FCM", "onesignal": "OneSignal",
	"hubspot": "HubSpot", "mailchimp": "Mailchimp", "klaviyo": "Klaviyo", "activecampaign": "ActiveCampaign",
	"zapier": "Zapier", "make": "Make", "n8n": "n8n",
}

// Name returns a provider's display name ("amazon-ses" → "Amazon SES"),
// or the slug itself for one Echo doesn't know.
func Name(provider string) string {
	if n, ok := names[provider]; ok {
		return n
	}
	return provider
}

// host reports whether r is for host or a subdomain of it.
func host(r Request, hosts ...string) bool {
	h := strings.ToLower(r.URL.Hostname())
	for _, want := range hosts {
		if h == want || strings.HasSuffix(h, "."+want) {
			return true
		}
	}
	return false
}

// generic is any HTTP call no recognizer claims: a webhook when it posts
// JSON or a form, plain HTTP otherwise.
var generic = Recognizer{
	Provider: "",
	Channel:  model.HTTP,
	Match:    func(Request) bool { return true },
	Fill: func(r Request, m *model.Message) {
		if r.Method == "POST" || r.Method == "PUT" || r.Method == "PATCH" {
			m.Channel = model.Webhook
		}
		m.To = []string{r.URL.Host + r.URL.Path}
	},
}

// Refuses reports whether rec refuses r, sealed or not (see Recognizer.Refuse).
func Refuses(rec Recognizer, r Request) bool {
	return rec.Refuse != nil && rec.Refuse(r)
}
