// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// maxVerifyPathWidth caps the column the file paths are padded to, so a
// deep path doesn't push every hash off the screen.
const maxVerifyPathWidth = 48

// WriteConsentVerify writes what consent verify found, as lines or as a
// consent-verify envelope. The lines (PRD 6.3), one per file then the
// signature and the verdict:
//
//	ok  consent.json  sha256 e3b0c442...b855
//	changed  report.html  want 5f70bf18... got 9a0c11de...
//	missing  evidence/page-1/reject-banner.png
//	Signature: good (ed25519, key team.pub)
//	Verified: 14 files match, signed by team.pub.
//
// When something doesn't match the last line is `Not verified: <n>
// changed, <m> missing, the signature doesn't match; <k> files match.`
// (only the parts that apply).
func WriteConsentVerify(w io.Writer, v *ConsentVerify, asJSON bool, o ConsentTextOptions) error {
	if v == nil {
		v = &ConsentVerify{}
	}
	if asJSON {
		return encodeJSON(w, verifyEnvelope(v))
	}
	width := 0
	for _, f := range v.Files {
		width = max(width, utf8.RuneCountInString(Safe(f.Path)))
	}
	width = min(width, maxVerifyPathWidth)
	var b strings.Builder
	ok, changed, missing := 0, 0, 0
	for _, f := range v.Files {
		path := Safe(f.Path)
		pad := path + strings.Repeat(" ", max(0, width-utf8.RuneCountInString(path)))
		switch {
		case f.OK:
			ok++
			fmt.Fprintf(&b, "ok  %s  sha256 %s\n", pad, shortHash(f.Got, true))
		case f.Got == "":
			missing++
			fmt.Fprintf(&b, "missing  %s\n", path)
		default:
			changed++
			fmt.Fprintf(&b, "changed  %s  want %s got %s\n", path, shortHash(f.Want, false), shortHash(f.Got, false))
		}
	}
	s := v.Signature
	key := Safe(s.Key)
	badSig := false
	switch {
	case !s.Present:
		b.WriteString("Signature: none\n")
	case s.Key == "":
		b.WriteString("Signature: present, no --key to check it\n")
	case s.OK:
		fmt.Fprintf(&b, "Signature: good (ed25519, key %s)\n", key)
	default:
		badSig = true
		fmt.Fprintf(&b, "Signature: doesn't match (ed25519, key %s)\n", key)
	}
	if changed+missing == 0 && !badSig {
		fmt.Fprintf(&b, "Verified: %s match", plural(ok, "file", "files"))
		if s.Present && s.OK && s.Key != "" {
			b.WriteString(", signed by " + key)
		}
		b.WriteString(".\n")
	} else {
		var why []string
		if changed > 0 {
			why = append(why, fmt.Sprintf("%d changed", changed))
		}
		if missing > 0 {
			why = append(why, fmt.Sprintf("%d missing", missing))
		}
		if badSig {
			why = append(why, "the signature doesn't match")
		}
		fmt.Fprintf(&b, "Not verified: %s; %s match.\n", strings.Join(why, ", "), plural(ok, "file", "files"))
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// shortHash writes a sha256 as its first 8 characters and, when tail is
// set, "..." and its last 4 (e3b0c442...b855); without tail it ends in
// "..." (e3b0c442...). A hash too short to shorten is written whole.
func shortHash(h string, tail bool) string {
	h = Safe(h)
	if len(h) <= 12 {
		return h
	}
	if tail {
		return h[:8] + "..." + h[len(h)-4:]
	}
	return h[:8] + "..."
}
