// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package consent_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
	"github.com/RhythmShahriar/echo/model"
)

// coreTypes are the types that are written to JSON, the envelope's data
// and the NDJSON events.
var coreTypes = []any{
	consent.Event{}, consent.Request{}, consent.Hit{}, consent.GoogleConsent{}, consent.GCS{}, consent.GCDSignal{}, consent.GCD{},
	consent.Signal{}, consent.TCF{}, consent.GPP{}, consent.Cookie{}, consent.StorageKey{}, consent.Visit{}, consent.CMPState{},
	consent.CMPInfo{}, consent.Locator{}, consent.Choice{}, consent.Script{}, consent.HitRef{}, consent.EchoMessage{}, consent.Run{},
	consent.ConsentMode{}, consent.Page{}, consent.Issue{}, consent.Ignore{}, consent.Inconclusive{}, consent.Change{},
	consent.Comparison{}, consent.EvidenceFile{}, consent.Evidence{}, consent.DataInfo{}, consent.Network{}, consent.Result{},
	consent.DetectResult{}, consent.HookRecord{},
}

// jsonKeys lists the JSON keys of struct type t with the Go field each
// comes from, embedded structs flattened as encoding/json does.
func jsonKeys(t reflect.Type, prefix string, out map[string][]string) {
	for i := range t.NumField() {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == "-" || !f.IsExported() {
			continue
		}
		if f.Anonymous && name == "" && f.Type.Kind() == reflect.Struct {
			jsonKeys(f.Type, prefix+f.Name+".", out)
			continue
		}
		if name == "" {
			name = f.Name
		}
		out[name] = append(out[name], prefix+f.Name)
	}
}

// TestJSONNoDuplicateKeys: no two fields of a core type write the same
// JSON key (encoding/json would silently drop both); every field
// has an explicit snake_case key, not its Go name.
func TestJSONNoDuplicateKeys(t *testing.T) {
	for _, v := range coreTypes {
		typ := reflect.TypeOf(v)
		keys := map[string][]string{}
		jsonKeys(typ, "", keys)
		for k, fields := range keys {
			if len(fields) > 1 {
				t.Errorf("%s: JSON key %q comes from %v", typ.Name(), k, fields)
			}
			if k != strings.ToLower(k) && typ != reflect.TypeOf(consent.HookRecord{}) {
				t.Errorf("%s: field %v has no snake_case JSON key (%q)", typ.Name(), fields, k)
			}
		}
	}
}

// The golden results decode into the core types with no unknown field
// and back out again with the same keys.
func TestGoldensDecode(t *testing.T) {
	for _, name := range consenttest.Goldens {
		r := consenttest.GoldenResult(name)
		if r.ID == "" || len(r.Pages) == 0 || len(r.Pages[0].Runs) == 0 {
			t.Errorf("%s: empty result", name)
		}
		for _, is := range r.Issues {
			if _, ok := consent.Check(is.Check); !ok {
				t.Errorf("%s: issue for unknown check %q", name, is.Check)
			}
			if is.Key != consent.IssueKey(is.Check, is.Page, is.Run, is.Tracker) {
				t.Errorf("%s: issue key %q doesn't match its fields", name, is.Key)
			}
		}
		again := consenttest.CloneResult(r)
		if !reflect.DeepEqual(again, r) {
			t.Errorf("%s: doesn't survive a JSON round trip", name)
		}
	}
}

func TestChecksCatalog(t *testing.T) {
	if len(consent.Checks) != 21 {
		t.Fatalf("%d checks; want 21", len(consent.Checks))
	}
	want := []string{consent.CheckBeforeChoice, consent.CheckAfterReject, consent.CheckAfterWithdraw, consent.CheckRejectGranted,
		consent.CheckRejectNotKept, consent.CheckRejectBroken, consent.CheckNoDefault, consent.CheckServerSide, consent.CheckScriptBefore,
		consent.CheckDefaultLate, consent.CheckV2Signals, consent.CheckAcceptNotGranted, consent.CheckNoFirstLayerReject,
		consent.CheckNoBanner, consent.CheckGPCIgnored, consent.CheckTCFMismatch, consent.CheckEmbed, consent.CheckCookielessPing,
		consent.CheckMetaNoConsentAPI, consent.CheckClickFallback, consent.CheckIgnoreExpired}
	levels := map[model.Level]int{}
	for i, c := range consent.Checks {
		if c.ID != want[i] {
			t.Errorf("check %d is %s; want %s (PRD 11.1 order)", i, c.ID, want[i])
		}
		if c.Doc == "" || c.Fix == "" || strings.Contains(c.Doc, "`") {
			t.Errorf("%s: Doc %q, Fix %q", c.ID, c.Doc, c.Fix)
		}
		levels[c.Level]++
	}
	if levels[model.Error] != 9 || levels[model.Warning] != 8 || levels[model.Notice] != 4 {
		t.Errorf("levels = %v; want 9 errors, 8 warnings, 4 notices", levels)
	}
	if c, ok := consent.Check(consent.CheckDefaultLate); !ok || c.Level != model.Warning {
		t.Errorf("Check(default-late) = %+v, %v", c, ok)
	}
	if _, ok := consent.Check("consent/nope"); ok {
		t.Error("Check found an unknown ID")
	}
}
