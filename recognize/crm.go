package recognize

import (
	"crypto/md5" //nolint:gosec // Mailchimp's subscriber hash is the MD5 of the address; not security
	"encoding/hex"
	"strings"

	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/rhythm/kit/secrets"
)

// CRMs and automation hooks: forms and orders pushed to HubSpot,
// Mailchimp, Klaviyo or ActiveCampaign, and catch hooks on Zapier, Make
// and n8n. They're all webhooks to the user; recognizing them names the
// destination and the event so compare can say "the Zapier hook stopped
// firing".
func init() {
	register(hubspot)
	register(mailchimp)
	register(klaviyo)
	register(activecampaign)
	register(zapier)
	register(makeHook)
	register(n8n)
}

// restEvent names a REST call from its path words (IDs dropped, joined
// with dots) and its verb: "objects.contacts create". The call targets one
// object when the path ends in an ID (lists/{id}/members is a collection).
func restEvent(segs []string, isID func(string) bool, method string) string {
	var words []string
	for _, s := range segs {
		if !isID(s) {
			words = append(words, s)
		}
	}
	withID := len(segs) > 0 && isID(segs[len(segs)-1])
	return strings.Join(words, ".") + " " + verb(method, withID)
}

// hookTo names a catch hook without its secret part: the URL is the
// credential for Zapier, Make and n8n hooks.
func hookTo(r Request) string {
	s := segments(r)
	if len(s) == 0 {
		return r.URL.Hostname()
	}
	s[len(s)-1] = secrets.Mask(s[len(s)-1], 2)
	return r.URL.Hostname() + "/" + strings.Join(s, "/")
}

// hubspot: the CRM API (api.hubapi.com/crm/v3/objects/{type}…) and form
// submissions (api.hsforms.com/submissions/v3/integration/submit/{portal}/{form}).
var hubspot = Recognizer{
	Provider: "hubspot",
	Channel:  model.Webhook,
	Match: func(r Request) bool {
		return host(r, "api.hubapi.com", "api.hubspot.com", "api.hsforms.com")
	},
	Fill: func(r Request, m *model.Message) {
		var p map[string]any
		r.JSON(&p)
		if host(r, "api.hsforms.com") {
			m.Event = "form submit"
			for _, f := range asList(p["fields"]) {
				if f := obj(f); strings.Contains(str(f["name"]), "email") {
					m.To = []string{str(f["value"])}
				}
			}
			return
		}
		s := segments(r)
		if len(s) > 2 && s[0] == "crm" {
			s = s[2:] // crm/v3
		}
		m.Event = restEvent(s, numeric.MatchString, r.Method)
		if e := str(dig(p, "properties", "email")); e != "" {
			m.To = []string{e}
		}
	},
	Fake: func(r Request, _ *model.Message) Fake {
		if host(r, "api.hsforms.com") {
			return jsonFake(200, `{"inlineMessage":"Thanks for submitting the form."}`)
		}
		p := r.URL.Path
		switch {
		case strings.Contains(p, "/batch/"):
			return jsonFake(201, js(map[string]any{"status": "COMPLETE", "results": []any{}, "startedAt": now(), "completedAt": now()}))
		case strings.HasSuffix(p, "/search"):
			return jsonFake(200, `{"total":0,"results":[]}`)
		case r.Method == "DELETE":
			return Fake{Status: 204}
		}
		var body map[string]any
		r.JSON(&body)
		props := obj(body["properties"])
		status := 200
		if r.Method == "POST" {
			status = 201
		}
		return jsonFake(status, js(map[string]any{"id": randDigits(11), "properties": props,
			"createdAt": now(), "updatedAt": now(), "archived": false}))
	},
}

// mailchimp: the Marketing API on {dc}.api.mailchimp.com/3.0, mostly
// list members added or updated by signup forms and shops.
var mailchimp = Recognizer{
	Provider: "mailchimp",
	Channel:  model.Webhook,
	Match: func(r Request) bool {
		return host(r, "api.mailchimp.com") && strings.HasPrefix(r.URL.Path, "/3.0/")
	},
	Fill: func(r Request, m *model.Message) {
		s := segments(r)[1:]
		// lists/{list}/members/{hash}: list IDs and hashes are hex.
		m.Event = restEvent(s, func(seg string) bool { return isHex(seg) && len(seg) >= 8 }, r.Method)
		var p map[string]any
		if r.JSON(&p) && str(p["email_address"]) != "" {
			m.To = []string{str(p["email_address"])}
		}
	},
	Fake: func(r Request, m *model.Message) Fake {
		s := segments(r)
		if len(s) >= 4 && s[1] == "lists" && s[3] == "members" && r.Method != "GET" && r.Method != "DELETE" {
			email := strings.ToLower(first(m.To))
			sum := md5.Sum([]byte(email)) //nolint:gosec // Mailchimp's subscriber hash
			var p map[string]any
			r.JSON(&p)
			status := str(p["status"])
			if status == "" {
				status = str(p["status_if_new"])
			}
			mf := obj(p["merge_fields"])
			return jsonFake(200, js(map[string]any{"id": hex.EncodeToString(sum[:]), "email_address": email,
				"unique_email_id": randHex(10), "status": status, "list_id": s[2], "merge_fields": mf,
				"timestamp_signup": "", "last_changed": now()}))
		}
		if r.Method == "DELETE" {
			return Fake{Status: 204}
		}
		return jsonFake(200, `{}`)
	},
}

// klaviyo: a.klaviyo.com. /api/events (server-side "Placed Order"),
// /api/profiles, /client/subscriptions from forms, and the legacy
// /api/track and /api/identify, which answer "1".
var klaviyo = Recognizer{
	Provider: "klaviyo",
	Channel:  model.Webhook,
	Match:    func(r Request) bool { return host(r, "a.klaviyo.com") },
	Fill: func(r Request, m *model.Message) {
		var p map[string]any
		r.JSON(&p)
		attrs := dig(p, "data", "attributes")
		if name := str(dig(attrs, "metric", "data", "attributes", "name")); name != "" {
			m.Event = name
		} else {
			m.Event = restEvent(segments(r), func(s string) bool { return len(s) >= 6 && !isWord(s) }, r.Method)
		}
		for _, e := range []any{dig(attrs, "profile", "data", "attributes", "email"), dig(attrs, "email")} {
			if s := str(e); s != "" {
				m.To = []string{s}
				break
			}
		}
	},
	Fake: func(r Request, _ *model.Message) Fake {
		p := r.URL.Path
		switch {
		case p == "/api/track" || p == "/api/identify":
			return textFake(200, "1")
		case r.Method == "GET":
			return jsonFake(200, `{"data":[],"links":{}}`)
		case strings.HasPrefix(p, "/api/profiles") && r.Method == "POST":
			var body map[string]any
			r.JSON(&body)
			return Fake{Status: 201, Headers: map[string]string{"content-type": "application/vnd.api+json"},
				Body: js(map[string]any{"data": map[string]any{"type": "profile", "id": strings.ToUpper(randHex(26)),
					"attributes": dig(body, "data", "attributes")}})}
		}
		return Fake{Status: 202}
	},
}

// activecampaign: {account}.api-us1.com/api/3/… (contacts, contact/sync,
// contactLists, contactTags, deals).
var activecampaign = Recognizer{
	Provider: "activecampaign",
	Channel:  model.Webhook,
	Match: func(r Request) bool {
		return host(r, "api-us1.com", "activehosted.com") && strings.HasPrefix(r.URL.Path, "/api/3/")
	},
	Fill: func(r Request, m *model.Message) {
		m.Event = restEvent(segments(r)[2:], numeric.MatchString, r.Method)
		var p map[string]any
		if r.JSON(&p) {
			if e := str(dig(p, "contact", "email")); e != "" {
				m.To = []string{e}
			}
		}
	},
	Fake: func(r Request, _ *model.Message) Fake {
		var p map[string]any
		r.JSON(&p)
		if r.Method == "GET" {
			res := segments(r)[2]
			return jsonFake(200, js(map[string]any{res: []any{}, "meta": map[string]string{"total": "0"}}))
		}
		if r.Method == "DELETE" {
			return jsonFake(200, `{}`)
		}
		out := map[string]any{}
		for k, v := range p { // {"contact": {...}} → {"contact": {..., "id": "123"}}
			o := map[string]any{}
			for kk, vv := range obj(v) {
				o[kk] = vv
			}
			o["id"], o["cdate"], o["udate"], o["links"] = randDigits(5), now(), now(), map[string]any{}
			out[k] = o
		}
		return jsonFake(201, js(out))
	},
}

// zapier: catch hooks, hooks.zapier.com/hooks/catch/{account}/{hook}/.
var zapier = Recognizer{
	Provider:  "zapier",
	SecretURL: true,
	Channel:   model.Webhook,
	Match:     func(r Request) bool { return host(r, "hooks.zapier.com") },
	Fill:      func(r Request, m *model.Message) { m.To = []string{hookTo(r)} },
	Fake: func(Request, *model.Message) Fake {
		return jsonFake(200, js(map[string]string{"attempt": uuid(), "id": uuid(), "request_id": uuid(), "status": "success"}))
	},
}

// makeHook: Make (formerly Integromat) custom webhooks,
// hook.{region}.make.com/{token}. They answer "Accepted" as text.
var makeHook = Recognizer{
	Provider:  "make",
	SecretURL: true,
	Channel:   model.Webhook,
	Match: func(r Request) bool {
		h := strings.ToLower(r.URL.Hostname())
		return strings.HasPrefix(h, "hook.") && (strings.HasSuffix(h, ".make.com") || strings.HasSuffix(h, ".integromat.com"))
	},
	Fill: func(r Request, m *model.Message) { m.To = []string{hookTo(r)} },
	Fake: func(Request, *model.Message) Fake { return textFake(200, "Accepted") },
}

// n8n: webhook nodes, {instance}/webhook/{path} (or /webhook-test/ while
// building). "/webhook/" alone is far too common a path to claim, so the
// host must look like n8n too: n8n Cloud (*.n8n.cloud), a self-hosted
// instance named for it (n8n.shop.com, shop-n8n.example.net) or one on
// n8n's default port, 5678.
var n8n = Recognizer{
	Provider:  "n8n",
	SecretURL: true,
	Channel:   model.Webhook,
	Match: func(r Request) bool {
		p := r.URL.Path
		if !strings.HasPrefix(p, "/webhook/") && !strings.HasPrefix(p, "/webhook-test/") {
			return false
		}
		return host(r, "n8n.cloud") || n8nHost(r.URL.Hostname()) || r.URL.Port() == "5678"
	},
	Fill: func(r Request, m *model.Message) { m.To = []string{hookTo(r)} },
	Fake: func(Request, *model.Message) Fake {
		return jsonFake(200, `{"message":"Workflow was started"}`)
	},
}

// n8nHost reports whether a host name has a label naming n8n.
func n8nHost(h string) bool {
	for _, label := range strings.Split(strings.ToLower(h), ".") {
		for _, part := range strings.FieldsFunc(label, func(c rune) bool { return c == '-' || c == '_' }) {
			if part == "n8n" {
				return true
			}
		}
	}
	return false
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func isHex(s string) bool {
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return s != ""
}

// isWord reports whether s is a lowercase path word (profiles, events,
// profile-subscription-bulk-create-jobs) rather than an ID.
func isWord(s string) bool {
	for _, c := range s {
		if (c < 'a' || c > 'z') && c != '-' && c != '_' {
			return false
		}
	}
	return true
}

func randDigits(n int) string {
	h := randHex(n)
	b := []byte(h)
	for i, c := range b {
		if c > '9' {
			b[i] = '0' + (c-'a')%10
		}
	}
	if b[0] == '0' {
		b[0] = '1'
	}
	return string(b)
}
