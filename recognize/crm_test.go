package recognize

import (
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/model"
)

var zapierHook = "https://hooks.zapier.com/hooks/catch/1234567/" + join("ab", "c9xyz") + "/"

var crmCases = []tcase{
	{name: "create contact", provider: "hubspot", status: 201,
		req: jsonReq("POST", "https://api.hubapi.com/crm/v3/objects/contacts", `{"properties":{"email":"ann@example.com","firstname":"Ann"}}`,
			"authorization", "Bearer "+join("pat", "-na1-", "00000000-0000-4000-8000-000000000000")),
		check: func(t *testing.T, m model.Message) {
			eq(t, "channel", m.Channel, model.Webhook)
			eq(t, "event", m.Event, "objects.contacts create")
			eqList(t, "to", m.To, "ann@example.com")
		}},
	{name: "update deal", provider: "hubspot", status: 200,
		req:   jsonReq("PATCH", "https://api.hubapi.com/crm/v3/objects/deals/123456789", `{"properties":{"dealstage":"closedwon"}}`),
		check: func(t *testing.T, m model.Message) { eq(t, "event", m.Event, "objects.deals update") }},
	{name: "form submission", provider: "hubspot", status: 200,
		req: jsonReq("POST", "https://api.hsforms.com/submissions/v3/integration/submit/1234567/0b6c-form", `{"fields":[{"name":"email","value":"ann@example.com"},{"name":"firstname","value":"Ann"}]}`),
		check: func(t *testing.T, m model.Message) {
			eq(t, "event", m.Event, "form submit")
			eqList(t, "to", m.To, "ann@example.com")
		}},
	{name: "add list member", provider: "mailchimp", status: 200,
		req: jsonReq("POST", "https://us21.api.mailchimp.com/3.0/lists/a1b2c3d4e5/members", `{"email_address":"Ann@Example.com","status":"subscribed","merge_fields":{"FNAME":"Ann"}}`),
		check: func(t *testing.T, m model.Message) {
			eq(t, "event", m.Event, "lists.members create")
			eqList(t, "to", m.To, "Ann@Example.com")
		}},
	{name: "upsert list member", provider: "mailchimp", status: 200,
		req:   jsonReq("PUT", "https://us21.api.mailchimp.com/3.0/lists/a1b2c3d4e5/members/0c8a0c8f8a6b4d3f2e1d0c9b8a7f6e5d", `{"email_address":"ann@example.com","status_if_new":"subscribed"}`),
		check: func(t *testing.T, m model.Message) { eq(t, "event", m.Event, "lists.members update") }},
	{name: "server event", provider: "klaviyo", status: 202,
		req: jsonReq("POST", "https://a.klaviyo.com/api/events", `{"data":{"type":"event","attributes":{"metric":{"data":{"type":"metric","attributes":{"name":"Placed Order"}}},
			"profile":{"data":{"type":"profile","attributes":{"email":"ann@example.com"}}},"properties":{"value":40}}}}`, "authorization", "Klaviyo-API-Key "+join("pk", "_0123456789abcdef")),
		check: func(t *testing.T, m model.Message) {
			eq(t, "event", m.Event, "Placed Order")
			eqList(t, "to", m.To, "ann@example.com")
		}},
	{name: "create profile", provider: "klaviyo", status: 201,
		req:   jsonReq("POST", "https://a.klaviyo.com/api/profiles", `{"data":{"type":"profile","attributes":{"email":"ann@example.com"}}}`),
		check: func(t *testing.T, m model.Message) { eq(t, "event", m.Event, "api.profiles create") }},
	{name: "legacy track", provider: "klaviyo", status: 200,
		req: formReq("https://a.klaviyo.com/api/track", "data=e30%3D")},
	{name: "contact sync", provider: "activecampaign", status: 201,
		req: jsonReq("POST", "https://shop.api-us1.com/api/3/contact/sync", `{"contact":{"email":"ann@example.com","firstName":"Ann"}}`, "api-token", "0123456789abcdef"),
		check: func(t *testing.T, m model.Message) {
			eq(t, "event", m.Event, "contact.sync create")
			eqList(t, "to", m.To, "ann@example.com")
		}},
	{name: "catch hook", provider: "zapier", status: 200,
		req: jsonReq("POST", zapierHook, `{"order":1042}`),
		check: func(t *testing.T, m model.Message) {
			if len(m.To) != 1 || !strings.HasPrefix(m.To[0], "hooks.zapier.com/hooks/catch/1234567/") || strings.Contains(m.To[0], "c9xyz") {
				t.Errorf("to = %q (hook secret must be masked)", m.To)
			}
		}},
	{name: "custom webhook", provider: "make", status: 200,
		req: jsonReq("POST", "https://hook.eu2.make.com/"+join("abcd", "efgh1234ijkl5678"), `{"order":1042}`)},
	{name: "webhook node", provider: "n8n", status: 200,
		req:   jsonReq("POST", "https://shop.app.n8n.cloud/webhook/new-order", `{"order":1042}`),
		check: func(t *testing.T, m model.Message) { eq(t, "channel", m.Channel, model.Webhook) }},
	{name: "self-hosted test webhook", provider: "n8n", status: 200,
		req: jsonReq("POST", "https://automation.shop.test/webhook-test/2f6a", `{}`)},
}

var crmNegatives = []negative{
	{"HubSpot website", "hubspot", req("GET", "https://www.hubspot.com/pricing", nil, "")},
	{"Mailchimp site", "mailchimp", req("GET", "https://mailchimp.com/", nil, "")},
	{"Mandrill is not Mailchimp marketing", "mailchimp", jsonReq("POST", "https://mandrillapp.com/api/1.0/messages/send", `{}`)},
	{"Klaviyo static", "klaviyo", req("GET", "https://static.klaviyo.com/onsite/js/klaviyo.js", nil, "")},
	{"ActiveCampaign tracking", "activecampaign", req("GET", "https://shop.api-us1.com/track", nil, "")},
	{"Zapier site", "zapier", req("GET", "https://zapier.com/apps", nil, "")},
	{"Make site", "make", req("GET", "https://www.make.com/en", nil, "")},
	{"Teams webhook is not n8n", "n8n", jsonReq("POST", "https://outlook.office.com/webhook/abc", `{"text":"x"}`)},
}
