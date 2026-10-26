// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package consent

import "encoding/json"

// MaxHookRecord is the most bytes one hook record may have; hooks.js
// cuts a longer one and sets Cut.
const MaxHookRecord = 4 << 10

// HookRecord is the record hooks.js sends through the binding: consent/browse
// writes it, consent/signal parses it. At most MaxHookRecord bytes.
type HookRecord struct {
	T           float64           `json:"t"`               // performance.timeOrigin + performance.now(), ms
	Src         string            `json:"src"`             // datalayer, gtag, fbq, uetq, tcf, gpp, click, gpc
	Args        []json.RawMessage `json:"args,omitempty"`  // the call's arguments: functions "[function]", DOM nodes "[node]", cycles "[cycle]", depth <= 8, strings <= 1024 chars
	Event       string            `json:"event,omitempty"` // tcf: tcloaded, cmpuishown, useractioncomplete; gpp: the event name
	TCString    string            `json:"tcString,omitempty"`
	GDPRApplies *bool             `json:"gdprApplies,omitempty"`
	GPPString   string            `json:"gppString,omitempty"`
	Cut         bool              `json:"cut,omitempty"` // truncated to fit
}
