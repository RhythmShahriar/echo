// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cmpstate

import (
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"testing"
)

func TestAPI(t *testing.T) {
	quoted := func(s string) []byte { b, _ := json.Marshal(s); return b }
	tests := []struct {
		parser  string
		raw     []byte
		decided bool
		cats    map[string]bool
		isNil   bool
	}{
		{"cookiebot", quoted(`{"stamp":"x","necessary":true,"preferences":false,"statistics":false,"marketing":false,"method":"explicit"}`), true,
			map[string]bool{"necessary": true, "preferences": false, "statistics": false, "marketing": false}, false},
		{"cookiebot", []byte(`{"stamp":"0","necessary":true,"method":null}`), false, map[string]bool{"necessary": true}, false},
		{"onetrust", quoted(`{"groups":",C0001,C0003,"}`), false, map[string]bool{"C0001": true, "C0003": true}, false},
		{"cookieyes", []byte(`{"activeLaw":"gdpr","categories":{"necessary":true,"analytics":false},"isUserActionCompleted":true,"consentID":"x"}`), true,
			map[string]bool{"necessary": true, "analytics": false}, false},
		{"usercentrics", []byte(`[{"id":"a","status":true},{"id":"b","status":false}]`), false, map[string]bool{"services_granted": true, "services_denied": true}, false},
		{"borlabs", quoted(`{"essential":["borlabs-cookie"],"marketing":[]}`), true, map[string]bool{"essential": true, "marketing": false}, false},
		{"borlabs", []byte(`null`), false, nil, true},
		{"didomi", []byte(`{"decided":true,"categories":{"cookies":false}}`), true, map[string]bool{"cookies": false}, false},
		{"iubenda", []byte(`{"something":"else"}`), false, nil, true},
		{"cookiebot", []byte(``), false, nil, true},
		{"cookiebot", []byte(`"not an object"`), false, nil, true},
	}
	for i, tt := range tests {
		got, err := API(tt.parser, tt.raw)
		if err != nil {
			t.Fatalf("%d: %v", i, err)
		}
		if tt.isNil {
			if got != nil {
				t.Errorf("%d %s: %+v, want nil", i, tt.parser, got)
			}
			continue
		}
		if got == nil || got.Source != "api" || got.CMP != tt.parser || got.Decided != tt.decided || !maps.Equal(got.Categories, tt.cats) {
			t.Errorf("%d %s: %+v", i, tt.parser, got)
		}
	}
	if _, err := API("cookiebot", []byte(strings.Repeat(" ", 10)+strings.Repeat("x", MaxValue+1))); !errors.Is(err, ErrUnreadable) {
		t.Errorf("too large: %v", err)
	}
}
