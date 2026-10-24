// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package recognize

import (
	"bytes"
	"errors"
	"io"
	"mime"

	"github.com/emersion/go-message/mail"

	"github.com/RhythmShahriar/echo/model"
)

// maxMIME caps how much of each MIME part is read: the body is stored
// separately, so the parsed fields only need enough to show and check.
const maxMIME = 4 << 20

// fillMIME sets the email fields of m from a raw RFC 5322 message, for
// APIs that take one whole (Gmail's raw, Graph's MIME sendMail). Fields
// already set are kept. It is a small copy of catcher.ParseMIME, which
// recognize can't import (catcher imports recognize); unlike SMTP, an
// API call carries Bcc in its headers, so that is read too.
func fillMIME(raw []byte, m *model.Message) {
	r, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		return
	}
	h := r.Header
	set := func(dst *string, v string) {
		if *dst == "" {
			*dst = v
		}
	}
	setList := func(dst *[]string, v []string) {
		if len(*dst) == 0 {
			*dst = v
		}
	}
	subject, _ := h.Subject()
	set(&m.Subject, subject)
	set(&m.From, first(mimeAddrs(h, "From")))
	set(&m.ReplyTo, first(mimeAddrs(h, "Reply-To")))
	setList(&m.To, mimeAddrs(h, "To"))
	setList(&m.CC, mimeAddrs(h, "Cc"))
	setList(&m.BCC, mimeAddrs(h, "Bcc"))
	for {
		p, err := r.NextPart()
		if errors.Is(err, io.EOF) || err != nil {
			return
		}
		ih, ok := p.Header.(*mail.InlineHeader)
		if !ok {
			continue // an attachment
		}
		ct, _, _ := mime.ParseMediaType(ih.Get("Content-Type"))
		b, err := io.ReadAll(io.LimitReader(p.Body, maxMIME))
		if err != nil {
			return
		}
		switch ct {
		case "text/html":
			set(&m.HTML, string(b))
		case "text/plain", "":
			set(&m.Text, string(b))
		}
	}
}

func mimeAddrs(h mail.Header, key string) []string {
	list, err := h.AddressList(key)
	if err != nil {
		if v := h.Get(key); v != "" {
			return []string{v}
		}
		return nil
	}
	out := make([]string, 0, len(list))
	for _, a := range list {
		out = append(out, a.Address)
	}
	return out
}
