// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package catcher

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/RhythmShahriar/echo/model"
)

// The wire versions this catcher accepts. Wire 2 adds source.site,
// email.attachments and signed run values to wire 1; a wire 1 capture
// still reads as before. The wire package this module pins still says 1,
// so the wire 2 fields are read here until it carries them.
const (
	wireMin = 1
	wireMax = 2
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
	case v > wireMax:
		return fmt.Errorf("%s (it speaks wire %d, this Echo wire %d at most)", Newer, v, wireMax)
	case v < 0:
		return fmt.Errorf("v: %d is not a wire version: send %d to %d", v, wireMin, wireMax)
	}
	return nil
}

// extras are the wire 2 fields of a capture. They're decoded from the
// same body as the wire.Capture, so a capture without them, or a wire 1
// one, leaves them empty.
type extras struct {
	Source struct {
		Site string `json:"site"`
	} `json:"source"`
	Email *struct {
		Attachments []wireAttachment `json:"attachments"`
	} `json:"email"`
}

// wireAttachment is one file a connector describes: never its content.
type wireAttachment struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// decodeExtras reads the wire 2 fields from a capture body that already
// decoded as a wire.Capture, so its errors were reported there.
func decodeExtras(body []byte) extras {
	var x extras
	_ = json.Unmarshal(body, &x)
	return x
}

// attachments returns the files x describes as model attachments, at
// most maxAttachments of them. A checksum that isn't a SHA-256 in hex is
// dropped rather than shown as one, a type that isn't a MIME type is
// dropped too and a negative size is read as unknown.
func (x extras) attachments() []model.Attachment {
	if x.Email == nil || len(x.Email.Attachments) == 0 {
		return nil
	}
	files := x.Email.Attachments[:min(len(x.Email.Attachments), maxAttachments)]
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

// sigLen is the length of a run signature: 16 bytes in unpadded base64url.
const sigLen = 22

// splitRun reads a capture's run value, "<run>/<action>" or the signed
// "<run>/<action>.<sig>" a runner sends when a token is set. A signed
// value counts only when it verifies with one of tokens: one that doesn't
// (tampered, or signed with a token this Echo doesn't have) is dropped,
// so the message is not credited to an action a visitor named. An
// unsigned value is taken as it is: the capture itself carries the token.
//
// This matches wire.VerifyRun in wire 2 and can call it once the wire
// package this module pins has it.
func splitRun(value string, tokens []string) (run, action string) {
	v, sig := value, ""
	if i := strings.LastIndexByte(value, '.'); i >= 0 && isSig(value[i+1:]) {
		v, sig = value[:i], value[i+1:]
	}
	run, action, _ = strings.Cut(v, "/")
	if sig == "" {
		return run, action
	}
	if run == "" {
		return "", ""
	}
	for _, t := range tokens {
		if t != "" && hmac.Equal([]byte(sig), []byte(runSig(t, v))) {
			return run, action
		}
	}
	return "", ""
}

// runSig is the first 16 bytes of HMAC-SHA256(token, v) in unpadded
// base64url.
func runSig(token, v string) string {
	m := hmac.New(sha256.New, []byte(token))
	m.Write([]byte(v))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:16])
}

// isSig reports whether s has the shape of a signature, so an action name
// with a dot in it ("order.paid") is not mistaken for a signed value.
func isSig(s string) bool {
	if len(s) != sigLen {
		return false
	}
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return false
		}
	}
	return true
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
