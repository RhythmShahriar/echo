// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"cmp"
	"encoding/json"
	"io"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/model"
)

// NewConsentEnvelope wraps a consent result: its own summary (PRD 9.3),
// Complete as the check set it and the verdict for failOn. The version
// is the Echo that made the result, else ToolVersion.
func NewConsentEnvelope(r *consent.Result, failOn string) ConsentEnvelope {
	return ConsentEnvelope{
		Schema: model.Schema, Kind: KindConsent, Tool: "rhythm-echo", Version: cmp.Or(r.Echo, ToolVersion),
		Complete: r.Complete, Summary: NewConsentSummary(r.Summary()), Verdict: r.Verdict(failOn), Data: r,
	}
}

// WriteConsentJSON writes a consent check in a ConsentEnvelope, its
// verdict for failOn.
func WriteConsentJSON(w io.Writer, r *consent.Result, failOn string) error {
	if r == nil {
		r = &consent.Result{}
	}
	return encodeJSON(w, NewConsentEnvelope(r, failOn))
}

// encodeJSON writes v indented with HTML left unescaped, as WriteJSON
// does, so captured URLs read as sent.
func encodeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// detectEnvelope wraps what consent detect found. A banner with both
// controls is ok; a banner with a control missing is findings with one
// warning per missing control; no banner is error and incomplete: the
// page couldn't be read, as exit 3 says.
func detectEnvelope(d *consent.DetectResult) Envelope {
	e := NewEnvelope(KindConsentDetect, d)
	switch {
	case !d.CMP.Found:
		e.Complete, e.Verdict = false, VerdictError
	case d.Accept == nil || d.Reject == nil:
		if d.Accept == nil {
			e.Summary.Warnings++
		}
		if d.Reject == nil {
			e.Summary.Warnings++
		}
		e.Verdict = VerdictFindings
	}
	return e
}

// verifyEnvelope wraps what consent verify found: a changed file or a
// signature that doesn't match is an error (findings); a missing file
// leaves the folder unchecked (error, incomplete), as exit 3 says.
func verifyEnvelope(v *ConsentVerify) Envelope {
	e := NewEnvelope(KindConsentVerify, v)
	missing := 0
	for _, f := range v.Files {
		switch {
		case f.OK:
		case f.Got == "":
			missing++
		default:
			e.Summary.Errors++
		}
	}
	if s := v.Signature; s.Present && s.Key != "" && !s.OK {
		e.Summary.Errors++
	}
	switch {
	case missing > 0:
		e.Summary.Errors += missing
		e.Complete, e.Verdict = false, VerdictError
	case e.Summary.Errors > 0:
		e.Verdict = VerdictFindings
	}
	return e
}
