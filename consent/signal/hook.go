// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package signal

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/mask"
)

// Limits on what a hook record may carry. hooks.js keeps a record within
// MaxHookRecord bytes and 8 levels of arguments, but the page can call
// the binding itself, so the decoder checks again.
const (
	// maxDepth is the record object, its args array and 8 levels of
	// arguments.
	maxDepth = 10
	// maxRegions and maxRegionLen bound a Consent Mode region list.
	maxRegions   = 300
	maxRegionLen = 10
	// maxWaitMS is the longest wait_for_update kept; Google's own tags
	// never wait longer than that.
	maxWaitMS = 30000
	// maxArguments bounds how many indexed keys an Arguments-like object
	// is read for.
	maxArguments = 8
	// cutSuffix ends a Raw that was cut.
	cutSuffix = " (cut)"
)

// errTooLarge and errTooDeep refuse records past the limits.
var (
	errTooLarge = errors.New("hook record too large")
	errTooDeep  = errors.New("hook record too deep")
)

// consentKeys are the Consent Mode signals a default or update may set.
var consentKeys = []string{"ad_storage", "analytics_storage", "ad_user_data", "ad_personalization",
	"functionality_storage", "personalization_storage", "security_storage"}

// setKeys are the gtag set calls that change what Google tags send
// without consent.
var setKeys = map[string]bool{"ads_data_redaction": true, "url_passthrough": true}

// Hook parses one hooks.js record (consent.HookRecord JSON) into a
// Signal: a Consent Mode default, update or set from gtag or dataLayer,
// a Meta Pixel consent or init call or a Microsoft UET consent call. It
// returns nil, nil for any other record (a dataLayer event push such as
// {event: "consent"} is not Consent Mode). Signal.AtMS is left 0 for the
// caller. Raw is the masked record, at most consent.MaxHookRecord bytes.
func (Decoder) Hook(record []byte) (*consent.Signal, error) {
	if len(record) > consent.MaxHookRecord {
		return nil, errTooLarge
	}
	if depth(record) > maxDepth {
		return nil, errTooDeep
	}
	var h consent.HookRecord
	if err := json.Unmarshal(record, &h); err != nil {
		return nil, fmt.Errorf("hook record: %w", err)
	}
	var (
		s   *consent.Signal
		raw = mask.JSON(string(record))
	)
	switch h.Src {
	case "gtag", "datalayer":
		s = consentMode(h.Src, unwrap(h.Args))
	case "fbq":
		s, raw = meta(h.Args, raw)
	case "uetq":
		s = uet(h.Args)
	}
	if s == nil {
		return nil, nil
	}
	s.Raw = cutRaw(consent.SafeText(raw, 0), h.Cut)
	return s, nil
}

// consentMode reads gtag('consent', 'default'|'update', {...}) and
// gtag('set', 'ads_data_redaction'|'url_passthrough', v), as gtag or a
// dataLayer push records them.
func consentMode(src string, args []json.RawMessage) *consent.Signal {
	if len(args) < 2 {
		return nil
	}
	verb, ok := str(args[0])
	if !ok {
		return nil
	}
	switch verb {
	case "consent":
		cmd, _ := str(args[1])
		if (cmd != "default" && cmd != "update") || len(args) < 3 {
			return nil
		}
		var obj map[string]json.RawMessage
		if json.Unmarshal(args[2], &obj) != nil || obj == nil {
			return nil
		}
		s := &consent.Signal{Source: src, Command: cmd, State: state(obj, consentKeys)}
		s.Region = regions(obj["region"])
		s.WaitForUpdateMS = waitMS(obj["wait_for_update"])
		return s
	case "set":
		if key, ok := str(args[1]); ok && setKeys[key] && len(args) >= 3 {
			return &consent.Signal{Source: src, Command: "set"}
		}
		// gtag('set', {ads_data_redaction: true}) sets the same.
		var obj map[string]json.RawMessage
		if json.Unmarshal(args[1], &obj) == nil {
			for k := range setKeys {
				if _, ok := obj[k]; ok {
					return &consent.Signal{Source: src, Command: "set"}
				}
			}
		}
	}
	return nil
}

// unwrap turns a dataLayer push of one array (['consent', ...]) or of an
// Arguments object ({"0": "consent", "1": "default", ...}, which is what
// gtag pushes) into the call's own arguments. Anything else is returned
// as it is.
func unwrap(args []json.RawMessage) []json.RawMessage {
	if len(args) != 1 {
		return args
	}
	var arr []json.RawMessage
	if json.Unmarshal(args[0], &arr) == nil && arr != nil {
		return arr
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(args[0], &obj) != nil {
		return args
	}
	if _, ok := obj["0"]; !ok {
		return args
	}
	var out []json.RawMessage
	for i := range maxArguments {
		v, ok := obj[strconv.Itoa(i)]
		if !ok {
			break
		}
		out = append(out, v)
	}
	return out
}

// state maps the keys of obj that are in keys to Granted or Denied; any
// other value is Unknown. Keys outside keys are dropped.
func state(obj map[string]json.RawMessage, keys []string) map[string]consent.Value {
	out := map[string]consent.Value{}
	for _, k := range keys {
		raw, ok := obj[k]
		if !ok {
			continue
		}
		v, _ := str(raw)
		switch consent.Value(v) {
		case consent.Granted, consent.Denied:
			out[k] = consent.Value(v)
		default:
			out[k] = consent.Unknown
		}
	}
	return out
}

// regions reads a region list: at most maxRegions codes such as DE or
// US-CA. An entry longer than maxRegionLen or with characters a region
// code doesn't have is dropped, so page text never reaches a report.
func regions(raw json.RawMessage) []string {
	var list []json.RawMessage
	if raw == nil || json.Unmarshal(raw, &list) != nil {
		return nil
	}
	var out []string
	for _, r := range list {
		if len(out) == maxRegions {
			break
		}
		v, ok := str(r)
		if ok && v != "" && len(v) <= maxRegionLen && strings.IndexFunc(v, notRegion) < 0 {
			out = append(out, v)
		}
	}
	return out
}

// notRegion reports whether r can't be part of an ISO 3166 code.
func notRegion(r rune) bool {
	return !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-')
}

// waitMS reads wait_for_update, clamped to 0 to maxWaitMS.
func waitMS(raw json.RawMessage) int {
	var f float64
	if raw == nil || json.Unmarshal(raw, &f) != nil || math.IsNaN(f) {
		return 0
	}
	return int(max(0, min(f, maxWaitMS)))
}

// str reads a JSON string.
func str(raw json.RawMessage) (string, bool) {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

// depth returns how deeply raw's arrays and objects nest, skipping
// strings. It reads raw once and doesn't allocate.
func depth(raw []byte) int {
	d, most := 0, 0
	inStr, esc := false, false
	for _, c := range raw {
		switch {
		case esc:
			esc = false
		case inStr:
			switch c {
			case '\\':
				esc = true
			case '"':
				inStr = false
			}
		case c == '"':
			inStr = true
		case c == '[' || c == '{':
			d++
			most = max(most, d)
		case c == ']' || c == '}':
			d--
		}
	}
	return most
}

// cutRaw caps s at consent.MaxHookRecord bytes on a rune boundary and
// marks it " (cut)" when it, or the record it came from, was cut.
func cutRaw(s string, cut bool) string {
	if len(s) > consent.MaxHookRecord {
		n := consent.MaxHookRecord - len(cutSuffix)
		for n > 0 && !utf8.RuneStart(s[n]) {
			n--
		}
		s, cut = s[:n], true
	}
	if cut {
		s += cutSuffix
	}
	return s
}
