// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package web

import (
	"bufio"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"strings"

	"github.com/RhythmShahriar/echo/model"
)

// Attachment is a file an email carries, as its MIME source names it.
type Attachment struct {
	Name string `json:"name"` // the part's file name, as stored
	Type string `json:"type"` // the part's MIME type
	Size int    `json:"size"` // bytes of the part as stored (still encoded, maybe cut)
}

// maxParts bounds the walk through a crafted message.
const maxParts = 200

// attachments lists the files in an email's MIME source: parts with a
// file name, or marked as an attachment. The stored source may be cut or
// masked, so this is a best effort: a part it can't read is skipped.
func attachments(m model.Message) []Attachment {
	if m.Channel != model.Email || m.Body == "" || !strings.Contains(strings.ToLower(m.Body), "multipart/") {
		return nil
	}
	r := textproto.NewReader(bufio.NewReader(strings.NewReader(m.Body)))
	h, err := r.ReadMIMEHeader()
	if err != nil && h == nil {
		return nil
	}
	var out []Attachment
	n := 0
	walk(h.Get("Content-Type"), r.R, &out, &n)
	return out
}

func walk(contentType string, body io.Reader, out *[]Attachment, n *int) {
	mt, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.HasPrefix(mt, "multipart/") || params["boundary"] == "" {
		return
	}
	mr := multipart.NewReader(body, params["boundary"])
	for *n < maxParts {
		p, err := mr.NextRawPart()
		if err != nil {
			return
		}
		*n++
		ct := p.Header.Get("Content-Type")
		if strings.HasPrefix(strings.ToLower(ct), "multipart/") {
			walk(ct, p, out, n)
			continue
		}
		disp, dp, _ := mime.ParseMediaType(p.Header.Get("Content-Disposition"))
		_, cp, _ := mime.ParseMediaType(ct)
		name := dp["filename"]
		if name == "" {
			name = cp["name"]
		}
		if name == "" && disp != "attachment" {
			continue
		}
		if name == "" {
			name = "unnamed"
		}
		size, _ := io.Copy(io.Discard, io.LimitReader(p, 64<<20))
		t, _, _ := mime.ParseMediaType(ct)
		if t == "" {
			t = "application/octet-stream"
		}
		*out = append(*out, Attachment{Name: name, Type: t, Size: int(size)})
	}
}
