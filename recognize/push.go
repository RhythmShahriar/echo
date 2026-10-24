// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package recognize

import (
	"strings"

	"github.com/RhythmShahriar/echo/model"
)

// Push notifications.
func init() {
	register(fcm)
	register(onesignal)
	register(apns)
}

// fcm: Firebase Cloud Messaging HTTP v1, POST
// /v1/projects/{project}/messages:send with one message for a device
// token, a topic or a condition.
var fcm = Recognizer{
	Provider: "fcm",
	Name:     "FCM",
	Channel:  model.Push,
	Hosts:    []string{"fcm.googleapis.com"},
	Match: func(r Request) bool {
		s := segments(r)
		return host(r, "fcm.googleapis.com") && len(s) == 4 && s[0] == "v1" && s[1] == "projects" && s[3] == "messages:send"
	},
	Fill: func(r Request, m *model.Message) {
		var p map[string]any
		if !r.JSON(&p) {
			return
		}
		msg := obj(p["message"])
		switch {
		case str(msg["token"]) != "":
			m.To = []string{str(msg["token"])}
		case str(msg["topic"]) != "":
			m.To = []string{"topic " + str(msg["topic"])}
		case str(msg["condition"]) != "":
			m.To = []string{"condition " + str(msg["condition"])}
		}
		m.Subject = str(dig(msg, "notification", "title"))
		m.Text = str(dig(msg, "notification", "body"))
	},
	Fake: func(r Request, _ *model.Message) Fake {
		return jsonFake(200, js(map[string]string{"name": "projects/" + segments(r)[2] + "/messages/0:" + randHex(16)}))
	},
}

// onesignal: POST /api/v1/notifications (or the newer /notifications)
// with headings and contents per language and a target (segments,
// subscription IDs, aliases, emails or phone numbers).
var onesignal = Recognizer{
	Provider: "onesignal",
	Name:     "OneSignal",
	Channel:  model.Push,
	Hosts:    []string{"onesignal.com"},
	Match: func(r Request) bool {
		return host(r, "onesignal.com") && r.Method == "POST" &&
			(r.URL.Path == "/api/v1/notifications" || r.URL.Path == "/notifications")
	},
	Fill: func(r Request, m *model.Message) {
		var p map[string]any
		if !r.JSON(&p) {
			return
		}
		for _, k := range []string{"include_subscription_ids", "include_player_ids", "include_external_user_ids",
			"include_email_tokens", "include_phone_numbers"} {
			m.To = append(m.To, strs(p[k])...)
		}
		for _, s := range strs(p["included_segments"]) {
			m.To = append(m.To, "segment "+s)
		}
		for k, v := range obj(p["include_aliases"]) {
			for _, id := range strs(v) {
				m.To = append(m.To, k+" "+id)
			}
		}
		m.Subject = str(dig(p, "headings", "en"))
		m.Text = str(dig(p, "contents", "en"))
		// OneSignal also sends email and SMS through the same endpoint.
		switch {
		case str(p["target_channel"]) == "email" || str(p["email_subject"]) != "":
			m.Channel, m.Subject = model.Email, str(p["email_subject"])
			m.HTML = str(p["email_body"])
		case str(p["target_channel"]) == "sms" || str(p["sms_from"]) != "":
			m.Channel, m.From = model.SMS, str(p["sms_from"])
		}
	},
	Fake: func(_ Request, m *model.Message) Fake {
		return jsonFake(200, js(map[string]any{"id": uuid(), "recipients": max(len(m.To), 1), "external_id": nil}))
	},
}

// apns: Apple Push Notification service, HTTP/2 POST /3/device/{token}
// with {"aps": {"alert": …}} and the app in apns-topic. Production and
// development are separate hosts. It answers 200 with no body and the
// notification's ID in apns-id.
var apns = Recognizer{
	Provider: "apns",
	Name:     "APNs",
	Channel:  model.Push,
	Hosts:    []string{"api.push.apple.com", "api.sandbox.push.apple.com", "api.development.push.apple.com"},
	Match: func(r Request) bool {
		s := segments(r)
		return host(r, "api.push.apple.com", "api.sandbox.push.apple.com", "api.development.push.apple.com") &&
			r.Method == "POST" && len(s) == 3 && s[0] == "3" && s[1] == "device"
	},
	Fill: func(r Request, m *model.Message) {
		m.To = []string{segments(r)[2]}
		if t := r.Header("apns-push-type"); t != "" && t != "alert" {
			m.Event = t
		}
		var p map[string]any
		if !r.JSON(&p) {
			return
		}
		switch a := dig(p, "aps", "alert").(type) {
		case string:
			m.Text = a
		case map[string]any:
			m.Subject, m.Text = str(a["title"]), str(a["body"])
		}
	},
	Fake: func(Request, *model.Message) Fake {
		return Fake{Status: 200, Headers: map[string]string{"apns-id": strings.ToUpper(uuid())}}
	},
}
