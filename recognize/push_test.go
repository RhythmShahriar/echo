// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package recognize

import (
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/model"
)

var pushCases = []tcase{
	{name: "v1 send", provider: "fcm", status: 200,
		req: jsonReq("POST", "https://fcm.googleapis.com/v1/projects/shop-app/messages:send", `{"message":{"token":"dGVzdC10b2tlbg","notification":{"title":"Order shipped","body":"Arrives Friday"}}}`),
		check: func(t *testing.T, m model.Message) {
			eq(t, "channel", m.Channel, model.Push)
			eqList(t, "to", m.To, "dGVzdC10b2tlbg")
			eq(t, "subject", m.Subject, "Order shipped")
			eq(t, "text", m.Text, "Arrives Friday")
		}},
	{name: "notification", provider: "onesignal", status: 200,
		req: jsonReq("POST", "https://api.onesignal.com/notifications", `{"app_id":"5eb5a37e-b458-11e3-ac11-000c2940e62c","included_segments":["Subscribed Users"],
			"headings":{"en":"Sale"},"contents":{"en":"20% off today"}}`),
		check: func(t *testing.T, m model.Message) {
			eqList(t, "to", m.To, "segment Subscribed Users")
			eq(t, "text", m.Text, "20% off today")
		}},
	{name: "alert", provider: "apns", status: 200,
		req: jsonReq("POST", "https://api.push.apple.com/3/device/"+apnsToken, `{"aps":{"alert":{"title":"Order shipped","body":"Arrives Friday"},"badge":1}}`,
			"apns-topic", "com.shop.app", "apns-push-type", "alert"),
		check: func(t *testing.T, m model.Message) {
			eq(t, "channel", m.Channel, model.Push)
			eqList(t, "to", m.To, apnsToken)
			eq(t, "subject", m.Subject, "Order shipped")
			eq(t, "text", m.Text, "Arrives Friday")
			eq(t, "event", m.Event, "")
		}},
	{name: "development, plain alert", provider: "apns", status: 200,
		req:   jsonReq("POST", "https://api.sandbox.push.apple.com:443/3/device/"+apnsToken, `{"aps":{"alert":"Your table is ready"}}`),
		check: func(t *testing.T, m model.Message) { eq(t, "text", m.Text, "Your table is ready") }},
	{name: "background push", provider: "apns", status: 200,
		req:   jsonReq("POST", "https://api.push.apple.com/3/device/"+apnsToken, `{"aps":{"content-available":1}}`, "apns-push-type", "background"),
		check: func(t *testing.T, m model.Message) { eq(t, "event", m.Event, "background") }},
}

// apnsToken is a device token: 64 hex characters.
var apnsToken = strings.Repeat("a1b2c3d4", 8)

var pushNegatives = []negative{
	{"FCM legacy topics API", "fcm", jsonReq("POST", "https://fcm.googleapis.com/v1/projects/p/topics", `{}`)},
	{"OneSignal apps", "onesignal", jsonReq("GET", "https://onesignal.com/api/v1/apps", "")},
	{"APNs legacy binary host", "apns", jsonReq("POST", "https://gateway.push.apple.com/3/device/abc", `{}`)},
	{"APNs without a device", "apns", jsonReq("POST", "https://api.push.apple.com/3/device", `{}`)},
}

func TestAPNsFakeHasAnID(t *testing.T) {
	r := jsonReq("POST", "https://api.push.apple.com/3/device/"+apnsToken, `{"aps":{"alert":"x"}}`)
	var m model.Message
	f := FakeFor(Recognize(r, &m), r, &m)
	if f.Status != 200 || f.Body != "" || len(f.Headers["apns-id"]) != 36 {
		t.Errorf("fake = %+v", f)
	}
}
