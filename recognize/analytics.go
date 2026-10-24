// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package recognize

import (
	"encoding/json"
	"strings"

	"github.com/RhythmShahriar/echo/model"
)

// Server-side analytics: GA4 Measurement Protocol and the Meta
// Conversions API. Both carry event names, which is what compare tracks
// ("purchase" went missing after an update).
func init() {
	register(ga4)
	register(metaCAPI)
}

// ga4: POST /mp/collect?measurement_id=G-…&api_secret=… (web) or
// firebase_app_id=… (apps) with {"client_id", "events": [{"name"}]}. The
// real endpoint answers 204 whatever it gets; /debug/mp/collect answers
// with validation messages.
var ga4 = Recognizer{
	Provider: "ga4",
	Name:     "GA4",
	Channel:  model.Analytics,
	Match: func(r Request) bool {
		return host(r, "google-analytics.com", "analytics.google.com") &&
			(r.URL.Path == "/mp/collect" || r.URL.Path == "/debug/mp/collect")
	},
	Fill: func(r Request, m *model.Message) {
		q := r.URL.Query()
		id := q.Get("measurement_id")
		if id == "" {
			id = q.Get("firebase_app_id")
		}
		if id != "" {
			m.To = []string{id}
		}
		var p struct {
			Events []struct {
				Name string `json:"name"`
			} `json:"events"`
		}
		if r.JSON(&p) {
			names := make([]string, 0, len(p.Events))
			for _, e := range p.Events {
				names = append(names, e.Name)
			}
			m.Event = strings.Join(names, ",")
		}
	},
	Fake: func(r Request, _ *model.Message) Fake {
		if strings.HasPrefix(r.URL.Path, "/debug/") {
			return jsonFake(200, `{"validationMessages":[]}`)
		}
		return Fake{Status: 204}
	},
}

// metaCAPI: POST graph.facebook.com/{version}/{pixel-id}/events with
// {"data": [{"event_name"}], "access_token"} as JSON or a form whose data
// field is JSON. Only the pixel and event names are kept: the access token
// is never copied into the message. Meta test events (test_event_code)
// still count towards the ad account, so a sealed site must never reach
// Meta; the fake simply reports every event received.
var metaCAPI = Recognizer{
	Provider: "meta-capi",
	Name:     "Meta Conversions API",
	Channel:  model.Analytics,
	Match: func(r Request) bool {
		return host(r, "graph.facebook.com") && r.Method == "POST" && graphPath(r, "events")
	},
	Fill: func(r Request, m *model.Message) {
		m.To = []string{graphID(r)}
		events := metaEvents(r)
		names := make([]string, 0, len(events))
		for _, e := range events {
			names = append(names, str(obj(e)["event_name"]))
		}
		m.Event = strings.Join(names, ",")
	},
	Fake: func(r Request, _ *model.Message) Fake {
		return jsonFake(200, js(map[string]any{"events_received": len(metaEvents(r)), "messages": []any{},
			"fbtrace_id": "A" + randHex(22)}))
	},
}

func metaEvents(r Request) []any {
	var p struct {
		Data []any `json:"data"`
	}
	if isJSON(r) {
		r.JSON(&p)
		return p.Data
	}
	_ = json.Unmarshal([]byte(values(r).Get("data")), &p.Data)
	return p.Data
}
