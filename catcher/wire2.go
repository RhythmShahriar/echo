// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package catcher

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/wire"
)

// Newer is how Echo refuses a capture from a newer wire version. The
// connectors fall back to the version health names when a 400 answer
// contains "newer than Echo", so the text must keep those words; details
// may follow it.
const Newer = "this connector is newer than Echo; run: rhythm update"

// checkVersion refuses a capture whose wire version Echo doesn't speak.
// A missing version (0) is read as 1: the first connectors left it out
// and Echo's own SMTP sink and proxy don't set it.
func checkVersion(v int) error {
	switch {
	case v > wire.Version:
		return fmt.Errorf("%s (it speaks wire %d, this Echo wire %d at most)", Newer, v, wire.Version)
	case v < 0:
		return fmt.Errorf("v: %d is not a wire version: send %d to %d", v, wire.MinVersion, wire.Version)
	}
	return nil
}

// attachments returns the files a wire 2 capture describes (never their
// content) as model attachments, at most maxAttachments of them. A
// checksum that isn't a SHA-256 in hex is dropped rather than shown as
// one, a type that isn't a MIME type is dropped too and a negative size
// is read as unknown.
func attachments(e *wire.Email) []model.Attachment {
	if e == nil || len(e.Attachments) == 0 {
		return nil
	}
	files := e.Attachments[:min(len(e.Attachments), maxAttachments)]
	out := make([]model.Attachment, 0, len(files))
	for _, a := range files {
		sum := strings.ToLower(a.SHA256)
		if b, err := hex.DecodeString(sum); err != nil || len(b) != sha256.Size {
			sum = ""
		}
		typ := strings.ToLower(strings.TrimSpace(a.Type))
		if len(typ) > 127 || !mimeType.MatchString(typ) {
			typ = ""
		}
		out = append(out, model.Attachment{Name: a.Name, Type: typ, Size: max(a.Size, 0), SHA256: sum})
	}
	return out
}

// maxAttachments is how many files of one email Echo keeps: far more than
// any real email attaches, few enough that a capture can't fill the inbox
// with entries.
const maxAttachments = 100

// mimeType is a MIME type without parameters: application/pdf.
var mimeType = regexp.MustCompile(`^[a-z0-9][a-z0-9!#$&^_.+-]*/[a-z0-9][a-z0-9!#$&^_.+-]*$`)

// splitRun reads a capture's run value, "<run>/<action>" or the signed
// "<run>/<action>.<sig>" a runner sends when a token is set. A signed
// value counts only when wire.VerifyRun verifies it with one of tokens:
// one that doesn't (tampered, or signed with a token this Echo doesn't
// have) is dropped, so the message is not credited to an action a
// visitor named. An unsigned value is taken as it is, unlike in
// VerifyRun: the capture itself carries the token.
func splitRun(value string, tokens []string) (run, action string) {
	if _, sig := wire.SplitRun(value); sig == "" {
		run, action, _ = strings.Cut(value, "/")
		return run, action
	}
	for _, t := range tokens {
		if t == "" {
			continue
		}
		if run, action, ok := wire.VerifyRun(t, value); ok {
			return run, action
		}
	}
	return "", ""
}

// runTokens are the tokens a run value from a capture may be signed with:
// the shared and read tokens and the write token of the capture's site
// (every write token for a capture that came in without one, such as
// mail handed to the SMTP sink).
func (s *Server) runTokens(site string) []string {
	out := []string{s.cfg.Token, s.cfg.ReadToken}
	for t, id := range s.cfg.WriteTokens {
		if site == "" || id == site {
			out = append(out, t)
		}
	}
	return out
}
