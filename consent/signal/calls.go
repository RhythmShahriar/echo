// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package signal

import (
	"encoding/json"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/mask"
)

// meta reads Meta Pixel calls: fbq('consent', 'revoke'|'grant') and
// fbq('init', id, ...), so the verdict can tell a revoke before the init
// from one after it. fbq('track', ...) and the rest return nil: the
// network hit is the evidence. For init the Raw is rebuilt from the
// masked pixel ID alone, since the further arguments are advanced
// matching data about the visitor.
func meta(args []json.RawMessage, raw string) (*consent.Signal, string) {
	if len(args) < 2 {
		return nil, raw
	}
	verb, _ := str(args[0])
	switch verb {
	case "consent":
		if cmd, _ := str(args[1]); cmd == "revoke" || cmd == "grant" {
			return &consent.Signal{Source: "fbq", Command: cmd}, raw
		}
	case "init":
		id, ok := str(args[1])
		if !ok {
			// A pixel ID written as a number.
			var n json.Number
			if json.Unmarshal(args[1], &n) != nil {
				return nil, raw
			}
			id = n.String()
		}
		if id == "" {
			return nil, raw
		}
		b, err := json.Marshal(struct {
			Src  string   `json:"src"`
			Args []string `json:"args"`
		}{"fbq", []string{"init", consent.MaskID(id)}})
		if err != nil {
			return nil, raw
		}
		return &consent.Signal{Source: "fbq", Command: "init"}, mask.Text(string(b))
	}
	return nil, raw
}

// uet reads Microsoft UET's uetq.push('consent', 'default'|'update',
// {ad_storage: ...}), the one signal UET takes.
func uet(args []json.RawMessage) *consent.Signal {
	if len(args) < 3 {
		return nil
	}
	verb, _ := str(args[0])
	cmd, _ := str(args[1])
	if verb != "consent" || (cmd != "default" && cmd != "update") {
		return nil
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(args[2], &obj) != nil || obj == nil {
		return nil
	}
	return &consent.Signal{Source: "uetq", Command: cmd, State: state(obj, []string{"ad_storage"})}
}
