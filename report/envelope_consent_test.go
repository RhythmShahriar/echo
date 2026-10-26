// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
)

// The consent kinds are listed and each has a schema.
func TestConsentKinds(t *testing.T) {
	for _, k := range []Kind{KindConsent, KindConsentCatalog, KindConsentDetect, KindConsentVerify} {
		if !contains(Kinds, k) {
			t.Errorf("%s isn't in Kinds", k)
		}
		if _, err := Schema(k); err != nil {
			t.Errorf("%s: %v", k, err)
		}
	}
}

func contains(ks []Kind, k Kind) bool {
	for _, x := range ks {
		if x == k {
			return true
		}
	}
	return false
}

// The consent summary writes every count, 0 included, under the PRD 9.3
// names and in that order.
func TestConsentSummaryJSON(t *testing.T) {
	b, err := json.Marshal(NewConsentSummary(consent.Summary{Errors: 2, Notices: 1, Pages: 1, Runs: 3, Trackers: 2, BeforeChoice: 1, AfterReject: 2, Held: 7}))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"broke":0,"new":0,"changed":0,"fixed":0,"errors":2,"warnings":0,"notices":1,"pages":1,"runs":3,"failed_runs":0,` +
		`"inconclusive_runs":0,"trackers":2,"before_choice":1,"after_reject":2,"held":7}`
	if string(b) != want {
		t.Errorf("summary JSON:\n got %s\nwant %s", b, want)
	}
}

// ConsentEnvelope has Envelope's exported fields with the same JSON
// keys in the same order; only the summary type differs.
func TestConsentEnvelopeMatchesEnvelope(t *testing.T) {
	keys := func(typ reflect.Type) []string {
		var out []string
		for i := range typ.NumField() {
			if f := typ.Field(i); f.IsExported() {
				out = append(out, f.Tag.Get("json"))
			}
		}
		return out
	}
	got, want := keys(reflect.TypeFor[ConsentEnvelope]()), keys(reflect.TypeFor[Envelope]())
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("ConsentEnvelope keys %v; Envelope keys %v", got, want)
	}
}

// Adding the consent counts left every other kind's summary as it was.
func TestOtherKindsSummaryUnchanged(t *testing.T) {
	b, err := json.Marshal(Summary{})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"broke":0,"new":0,"changed":0,"fixed":0,"errors":0,"warnings":0,"notices":0,"actions":0,"messages":0,"failed_actions":0}`; string(b) != want {
		t.Errorf("Summary JSON = %s", b)
	}
}
