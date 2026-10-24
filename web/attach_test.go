// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package web

import (
	"testing"

	"github.com/RhythmShahriar/echo/model"
)

const invoiceMIME = "From: shop@shop.test\r\nTo: ann@example.org\r\nSubject: Invoice\r\nMIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=\"outer\"\r\n\r\n" +
	"--outer\r\nContent-Type: multipart/alternative; boundary=\"inner\"\r\n\r\n" +
	"--inner\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nYour invoice is attached.\r\n" +
	"--inner\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>Your invoice is attached.</p>\r\n" +
	"--inner--\r\n" +
	"--outer\r\nContent-Type: application/pdf; name=\"invoice-42.pdf\"\r\nContent-Disposition: attachment; filename=\"invoice-42.pdf\"\r\n" +
	"Content-Transfer-Encoding: base64\r\n\r\nJVBERi0xLjQK\r\n" +
	"--outer--\r\n"

func TestAttachments(t *testing.T) {
	got := attachments(model.Message{Channel: model.Email, Body: invoiceMIME})
	if len(got) != 1 || got[0] != (Attachment{Name: "invoice-42.pdf", Type: "application/pdf", Size: 12}) {
		t.Errorf("attachments = %+v", got)
	}
	for name, m := range map[string]model.Message{
		"plain email":    {Channel: model.Email, Body: "Subject: Hi\r\nContent-Type: text/plain\r\n\r\nHello\r\n"},
		"no source":      {Channel: model.Email, Text: "Hello"},
		"webhook":        {Channel: model.Webhook, Body: invoiceMIME},
		"cut boundary":   {Channel: model.Email, Body: "Content-Type: multipart/mixed; boundary=x\r\n\r\n--x\r\nContent-Type: text/plain\r\n\r\nHel"},
		"bad media type": {Channel: model.Email, Body: "Content-Type: multipart/;;\r\n\r\n"},
	} {
		if got := attachments(m); len(got) != 0 {
			t.Errorf("%s: %+v", name, got)
		}
	}
}
