// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package recognize

import (
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/model"
)

var metaToken = join("EAA", "Bsbc", "ZCZAx0123456789AbCdEfGhIjKlMnOp")

var analyticsCases = []tcase{
	{name: "measurement protocol", provider: "ga4", status: 204,
		req: jsonReq("POST", "https://www.google-analytics.com/mp/collect?measurement_id=G-ABC123&api_secret=Zx81kQpLmN0aBcDe", `{"client_id":"1.2","events":[{"name":"purchase","params":{"value":40}},{"name":"add_payment_info"}]}`),
		check: func(t *testing.T, m model.Message) {
			eq(t, "channel", m.Channel, model.Analytics)
			eq(t, "event", m.Event, "purchase,add_payment_info")
			eqList(t, "to", m.To, "G-ABC123")
		}},
	{name: "debug endpoint", provider: "ga4", status: 200,
		req:   jsonReq("POST", "https://region1.google-analytics.com/debug/mp/collect?firebase_app_id=1:23:android:45&api_secret=x", `{"app_instance_id":"a","events":[{"name":"sign_up"}]}`),
		check: func(t *testing.T, m model.Message) { eq(t, "event", m.Event, "sign_up") }},
	{name: "conversions JSON", provider: "meta-capi", status: 200,
		req: jsonReq("POST", "https://graph.facebook.com/v21.0/1234567890/events", `{"data":[{"event_name":"Purchase","event_time":1761264000,"user_data":{"em":["abc"]}},{"event_name":"Lead"}],
			"test_event_code":"TEST123","access_token":"`+metaToken+`"}`),
		check: func(t *testing.T, m model.Message) {
			eq(t, "event", m.Event, "Purchase,Lead")
			eqList(t, "to", m.To, "1234567890")
			if strings.Contains(strings.Join(m.To, "")+m.Event+m.Text+m.Subject, "EAA") {
				t.Error("access token copied into the message")
			}
		}},
	{name: "conversions form", provider: "meta-capi", status: 200,
		req:   formReq("https://graph.facebook.com/v19.0/1234567890/events", "data=%5B%7B%22event_name%22%3A%22AddToCart%22%7D%5D&access_token="+metaToken),
		check: func(t *testing.T, m model.Message) { eq(t, "event", m.Event, "AddToCart") }},
}

var analyticsNegatives = []negative{
	{"Universal Analytics collect", "ga4", formReq("https://www.google-analytics.com/collect", "v=1&t=pageview")},
	{"Graph page feed", "meta-capi", jsonReq("POST", "https://graph.facebook.com/v21.0/1234567890/feed", `{}`)},
}
