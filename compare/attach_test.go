// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package compare

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/model"
)

// file is an attachment for mimeEmail: its Content-Type and
// Content-Disposition lines and its bytes.
type file struct {
	header string
	data   []byte
}

func pdf(name string, data string) file {
	return file{fmt.Sprintf("Content-Type: application/pdf; name=%q\r\nContent-Disposition: attachment; filename=%q", name, name), []byte(data)}
}

// mimeEmail is an email as the SMTP sink stores it: the raw message, a
// text part and the files base64-encoded in multipart/mixed.
func mimeEmail(files ...file) model.Message {
	var b strings.Builder
	b.WriteString("From: shop@shop.com\r\nTo: buyer@example.com\r\nSubject: Your invoice\r\nMIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: multipart/mixed; boundary=\"b1\"\r\n\r\n--b1\r\nContent-Type: text/plain\r\n\r\nYour invoice is attached.\r\n")
	for _, f := range files {
		enc := base64.StdEncoding.EncodeToString(f.data)
		var lines []string
		for len(enc) > 76 {
			lines, enc = append(lines, enc[:76]), enc[76:]
		}
		lines = append(lines, enc)
		fmt.Fprintf(&b, "--b1\r\n%s\r\nContent-Transfer-Encoding: base64\r\n\r\n%s\r\n", f.header, strings.Join(lines, "\r\n"))
	}
	b.WriteString("--b1--\r\n")
	m := email("buyer@example.com", "Your invoice", "Your invoice is attached.")
	m.Body = b.String()
	return m
}

func TestAttachmentsFromMIME(t *testing.T) {
	data := strings.Repeat("%PDF-1.7 ", 9357) // 84213 bytes
	sum := sha256.Sum256([]byte(data))
	got := Attachments(mimeEmail(pdf("invoice-42.pdf", data),
		file{"Content-Type: image/png\r\nContent-Disposition: inline; filename*=UTF-8''r%C3%A9sum%C3%A9.png", []byte("png")}))
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if a := got[0]; a.Name != "invoice-42.pdf" || a.Type != "application/pdf" || a.Size != 84213 || a.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("pdf: %+v", a)
	}
	if got[0].String() != "invoice-42.pdf (application/pdf, 84 KB)" {
		t.Errorf("String: %s", got[0])
	}
	if got[1].Name != "résumé.png" || got[1].Size != 3 {
		t.Errorf("an RFC 2231 name: %+v", got[1])
	}
	// No attachments: a plain email, or a body that isn't MIME at all.
	if a := Attachments(mimeEmail()); len(a) != 0 {
		t.Errorf("text only: %+v", a)
	}
	if a := Attachments(email("a@b.com", "x", "x")); len(a) != 0 {
		t.Errorf("no body: %+v", a)
	}
}

func TestAttachmentsNestedAndPlaceholders(t *testing.T) {
	hash := strings.Repeat("ab", 32)
	m := email("buyer@example.com", "Invoice", "x")
	m.Body = "From: a@b.com\r\nContent-Type: multipart/mixed; boundary=out\r\n\r\n--out\r\n" +
		"Content-Type: multipart/alternative; boundary=in\r\n\r\n--in\r\nContent-Type: text/plain\r\n\r\nx\r\n--in--\r\n" +
		"--out\r\nContent-Type: application/pdf; name=\"invoice-42.pdf\"\r\nContent-Disposition: attachment\r\nContent-Transfer-Encoding: 8bit\r\n\r\n" +
		"[attachment invoice-42.pdf application/pdf 84211 sha256:" + hash + "]\r\n--out--\r\n"
	got := Attachments(m)
	if len(got) != 1 || got[0].Size != 84211 || got[0].SHA256 != hash {
		t.Errorf("placeholder in a part: %+v", got)
	}
	m.Body = "[attachment invoice 42.pdf application/pdf cut]"
	if got := Attachments(m); len(got) != 1 || got[0].Name != "invoice 42.pdf" || got[0].Size != -1 || got[0].String() != "invoice 42.pdf (application/pdf)" {
		t.Errorf("a cut placeholder: %+v", got)
	}
	// An email cut inside its attachment: the file is there, its size isn't known.
	m = mimeEmail(pdf("invoice-42.pdf", strings.Repeat("x", 3000)))
	m.Body = m.Body[:len(m.Body)/2] + "\n" + truncatedMark
	if got := Attachments(m); len(got) != 1 || got[0].Size != -1 {
		t.Errorf("cut: %+v", got)
	}
}

func attachmentChanges(b, a model.Message) string {
	var out []string
	for _, c := range Messages([]model.Message{b}, []model.Message{a}) {
		if strings.HasPrefix(c.Check, "message/attachment") {
			out = append(out, c.Class+" "+c.Check+": "+c.Title)
		}
	}
	return strings.Join(out, "\n")
}

func TestAttachmentChanges(t *testing.T) {
	inv := pdf("invoice-42.pdf", strings.Repeat("%PDF ", 16843)) // 84215 bytes
	for _, tc := range []struct {
		name   string
		before []file
		after  []file
		want   string
	}{
		{"lost", []file{inv}, nil,
			"broke message/attachment-lost: Email to buyer@example.com no longer attaches invoice-42.pdf (application/pdf, 84 KB)"},
		{"still attached", []file{inv}, []file{inv}, ""},
		{"changed", []file{inv}, []file{pdf("invoice-42.pdf", "%PDF other")},
			"changed message/attachment-changed: Email to buyer@example.com: attachment invoice-42.pdf changed"},
		{"empty now", []file{inv}, []file{pdf("invoice-42.pdf", "")},
			"broke message/attachment-changed: Email to buyer@example.com: attachment invoice-42.pdf is empty"},
		{"added", nil, []file{inv},
			"new message/attachment-new: Email to buyer@example.com now attaches invoice-42.pdf (application/pdf, 84 KB)"},
		{"none either side", nil, nil, ""},
		{"renamed by number", []file{inv}, []file{pdf("invoice-43.pdf", "%PDF another order")}, ""},
		{"renamed", []file{inv}, []file{pdf("receipt-42.pdf", "%PDF")},
			"broke message/attachment-lost: Email to buyer@example.com no longer attaches invoice-42.pdf (application/pdf, 84 KB)\n" +
				"new message/attachment-new: Email to buyer@example.com now attaches receipt-42.pdf (application/pdf, 4 bytes)"},
		{"other type", []file{inv}, []file{{"Content-Type: text/html; name=\"invoice-42.pdf\"", []byte("<p>")}},
			"changed message/attachment-changed: Email to buyer@example.com: attachment invoice-42.pdf changed type"},
	} {
		if got := attachmentChanges(mimeEmail(tc.before...), mimeEmail(tc.after...)); got != tc.want {
			t.Errorf("%s:\n got %s\nwant %s", tc.name, got, tc.want)
		}
	}
}

func TestByteSize(t *testing.T) {
	for n, want := range map[int64]string{0: "0 bytes", 1: "1 byte", 999: "999 bytes", 84211: "84 KB", 1_400_000: "1.4 MB"} {
		if got := byteSize(n); got != want {
			t.Errorf("byteSize(%d) = %s, want %s", n, got, want)
		}
	}
}
