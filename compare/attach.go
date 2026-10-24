// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package compare

import (
	"bufio"
	"cmp"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"regexp"
	"strconv"
	"strings"

	"github.com/RhythmShahriar/echo/model"
)

// Attachment is a file an email carries: what compare checks is still
// attached and whether it changed.
type Attachment struct {
	Name   string
	Type   string // application/pdf
	Size   int64  // decoded bytes; -1 when the stored email was cut inside it
	SHA256 string // hex; "" when the size is unknown
}

// String is "invoice-42.pdf (application/pdf, 84 KB)".
func (a Attachment) String() string {
	if a.Size < 0 {
		return fmt.Sprintf("%s (%s)", a.Name, a.Type)
	}
	return fmt.Sprintf("%s (%s, %s)", a.Name, a.Type, byteSize(a.Size))
}

// byteSize is a size as people read it: "512 bytes", "84 KB", "1.4 MB".
func byteSize(n int64) string {
	switch {
	case n == 1:
		return "1 byte"
	case n < 1000:
		return fmt.Sprintf("%d bytes", n)
	case n < 1000*1000:
		return fmt.Sprintf("%d KB", (n+500)/1000)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/1e6)
}

// placeholderRe is the line the catcher stores instead of an attachment's
// bytes: "[attachment invoice-42.pdf application/pdf 84211 sha256:…]", or
// "[attachment invoice-42.pdf application/pdf cut]" when the stored email
// ends inside it.
var placeholderRe = regexp.MustCompile(`\[attachment (.+?) ([\w.+-]+/[\w.+-]+) (?:(\d+) sha256:([0-9a-f]{64})|cut)\]`)

// Attachments lists the files an email carries, from its raw MIME body:
// every part with a file name, or that says it is an attachment. A part
// the catcher replaced by its placeholder line gives the placeholder's
// facts. Other channels carry none.
func Attachments(m model.Message) []Attachment {
	if m.Channel != model.Email || strings.TrimSpace(m.Body) == "" {
		return nil
	}
	cut := truncated(m)
	raw := strings.TrimSuffix(m.Body, truncatedMark)
	msg, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		return placeholders(raw)
	}
	var out []Attachment
	walkParts(textproto.MIMEHeader(msg.Header), msg.Body, cut, &out, 0)
	if len(out) == 0 {
		return placeholders(raw)
	}
	return out
}

// placeholders reads the catcher's placeholder lines alone, for a body
// that isn't a whole MIME message.
func placeholders(s string) []Attachment {
	var out []Attachment
	for _, p := range placeholderRe.FindAllStringSubmatch(s, -1) {
		out = append(out, fromPlaceholder(p))
	}
	return out
}

func fromPlaceholder(p []string) Attachment {
	a := Attachment{Name: p[1], Type: strings.ToLower(p[2]), Size: -1}
	if p[3] != "" {
		a.Size, _ = strconv.ParseInt(p[3], 10, 64)
		a.SHA256 = p[4]
	}
	return a
}

// maxDepth bounds nested multiparts: a crafted email can't recurse
// forever.
const maxDepth = 8

// walkParts adds the attachments of one MIME entity to out. cut says the
// stored email was shortened, so a part that ends early has an unknown
// size rather than a smaller one.
func walkParts(h textproto.MIMEHeader, body io.Reader, cut bool, out *[]Attachment, depth int) {
	ct, params, err := mime.ParseMediaType(h.Get("Content-Type"))
	if err != nil {
		ct, params = "text/plain", nil
	}
	if strings.HasPrefix(ct, "multipart/") {
		if depth >= maxDepth || params["boundary"] == "" {
			return
		}
		mr := multipart.NewReader(body, params["boundary"])
		for {
			p, err := mr.NextRawPart()
			if err != nil {
				return // the end, or the email was cut here
			}
			walkParts(p.Header, p, cut, out, depth+1)
		}
	}
	disp, dparams, _ := mime.ParseMediaType(h.Get("Content-Disposition"))
	name := decodeWord(cmp.Or(dparams["filename"], params["name"]))
	if name == "" && disp != "attachment" {
		return // a text or HTML body part
	}
	if name == "" {
		name = "unnamed"
	}
	data, err := io.ReadAll(decoder(h.Get("Content-Transfer-Encoding"), body))
	if p := placeholderRe.FindStringSubmatch(string(data)); p != nil && len(data) < 512 {
		*out = append(*out, fromPlaceholder(p))
		return
	}
	a := Attachment{Name: name, Type: strings.ToLower(ct), Size: -1}
	// A part cut short ends without its boundary, which is an error; a
	// message that is one attachment can only be cut at its end.
	if err == nil && (!cut || depth > 0) {
		sum := sha256.Sum256(data)
		a.Size, a.SHA256 = int64(len(data)), hex.EncodeToString(sum[:])
	}
	*out = append(*out, a)
}

func decoder(encoding string, r io.Reader) io.Reader {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, &spaceless{r: bufio.NewReader(r)})
	case "quoted-printable":
		return quotedprintable.NewReader(r)
	}
	return r
}

// spaceless drops the line breaks and spaces base64 bodies are wrapped
// with, which the decoder doesn't accept.
type spaceless struct{ r io.ByteReader }

func (s *spaceless) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		c, err := s.r.ReadByte()
		if err != nil {
			return n, err
		}
		if c != '\r' && c != '\n' && c != ' ' && c != '\t' {
			p[n] = c
			n++
		}
	}
	return n, nil
}

// decodeWord decodes an RFC 2047 name ("=?UTF-8?B?…?="); RFC 2231 names
// (filename*=) are decoded by mime.ParseMediaType already.
func decodeWord(s string) string {
	if d, err := new(mime.WordDecoder).DecodeHeader(s); err == nil {
		return d
	}
	return s
}

// attachmentKey matches an attachment across runs: its name without
// numbers, in any case, so invoice-42.pdf in the snapshot is
// invoice-43.pdf now.
func attachmentKey(a Attachment) string { return strings.ToLower(digits.ReplaceAllString(a.Name, "#")) }

// attachments compares the files two matched emails carry.
func attachments(b, a model.Message, what string) []model.Change {
	ba, aa := Attachments(b), Attachments(a)
	if len(ba) == 0 && len(aa) == 0 {
		return nil
	}
	at := map[string][]int{}
	for i, x := range aa {
		at[attachmentKey(x)] = append(at[attachmentKey(x)], i)
	}
	used := make([]bool, len(aa))
	var out []model.Change
	for _, x := range ba {
		k := attachmentKey(x)
		idx := at[k]
		if len(idx) == 0 {
			out = append(out, model.Change{Class: model.Broke, Check: "message/attachment-lost", Channel: a.Channel,
				Title:  fmt.Sprintf("%s no longer attaches %s", what, x.Name),
				Fix:    "Something stopped attaching the file: check the plugin's attachment setting and that the file is still made.",
				Before: x.String(), After: "not attached"})
			continue
		}
		at[k] = idx[1:]
		used[idx[0]] = true
		if c, changed := attachmentChange(x, aa[idx[0]], what); changed {
			c.Channel = a.Channel
			out = append(out, c)
		}
	}
	for i, x := range aa {
		if !used[i] {
			out = append(out, model.Change{Class: model.New, Check: "message/attachment-new", Channel: a.Channel,
				Title: fmt.Sprintf("%s now attaches %s", what, x.Name), Before: "not attached", After: x.String()})
		}
	}
	return out
}

// attachmentChange compares one attachment across runs. An empty file is
// BROKE. Other changes count only when the name is the same: a file
// named after the order (invoice-43.pdf) differs from the snapshot's
// (invoice-42.pdf) every run. New bytes alone aren't a change either:
// generated files (a PDF invoice above all) embed their creation time and
// an ID, so their hash differs on every run. The file changed when its
// size moves by more than a tenth.
func attachmentChange(b, a Attachment, what string) (model.Change, bool) {
	if b.Size < 0 || a.Size < 0 {
		return model.Change{}, false // cut: nothing to compare
	}
	if a.Size == 0 && b.Size > 0 {
		return model.Change{Class: model.Broke, Check: "message/attachment-changed", Title: fmt.Sprintf("%s: attachment %s is empty", what, a.Name),
			Fix:    "The file is attached but has nothing in it: check how it's made (a PDF library error, a missing template).",
			Before: byteSize(b.Size), After: "0 bytes"}, true
	}
	switch {
	case b.Type != a.Type:
		return model.Change{Class: model.Changed, Check: "message/attachment-changed", Title: fmt.Sprintf("%s: attachment %s changed type", what, a.Name),
			Before: b.Type, After: a.Type, Fix: "Check the file still opens where it's read; if the change is intended, take a new snapshot."}, true
	case b.Name != a.Name:
		return model.Change{}, false
	case sizeMoved(b.Size, a.Size):
		return model.Change{Class: model.Changed, Check: "message/attachment-changed", Title: fmt.Sprintf("%s: attachment %s changed", what, a.Name),
			Before: byteSize(b.Size) + ", sha256 " + b.SHA256[:min(12, len(b.SHA256))],
			After:  byteSize(a.Size) + ", sha256 " + a.SHA256[:min(12, len(a.SHA256))],
			Fix:    "Open the new file; if the change is intended, take a new snapshot."}, true
	}
	return model.Change{}, false
}

// sizeMoved reports whether an attachment's size changed by more than a
// tenth of what it was, which a new timestamp or ID inside it doesn't do.
func sizeMoved(before, after int64) bool {
	d := after - before
	if d < 0 {
		d = -d
	}
	return d*10 > before
}

// attachmentGroupKey is groupKey for attachment changes: the title with
// its numbers as # (invoice-42.pdf and invoice-43.pdf are the same file
// from run to run) and without the sizes and hashes in Before and After.
// The same lost file then groups across actions and its Key still
// accepts it on the next run.
func attachmentGroupKey(c model.Change) string {
	return c.Class + "|" + c.Check + "|" + strings.ToLower(digits.ReplaceAllString(c.Title, "#"))
}
