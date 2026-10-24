// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package catcher

import (
	"net/http/httptest"
	"os"
	"testing"

	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/wire"
)

const (
	pdfSHA = "56c2ac043c28d3543275a6f50b593a6beb0b6888ed8159ac02e3e7f4a32ccb26"
	pngSHA = "987c39f47f3c4f9f37e8bc8561602af9a49a2a29e193cd2d50b7e0b942517655"
)

func TestParseMIMEAttachments(t *testing.T) {
	pdf := model.Attachment{Name: "invoice-42.pdf", Type: "application/pdf", Size: 77, SHA256: pdfSHA}
	tests := []struct {
		file  string
		text  string
		html  string
		files []model.Attachment
	}{
		{"pdf.eml", "Your invoice is attached.", "", []model.Attachment{pdf}},
		{"inline.eml", "", `<p><img src="cid:logo"> Welcome!</p>`, []model.Attachment{
			{Type: "image/png", Size: 49, SHA256: pngSHA, Inline: true}, // unnamed, by Content-ID
			{Name: "banner.png", Type: "image/png", Size: 49, SHA256: pngSHA, Inline: true},
		}},
		{"nested.eml", "Thanks for your order.", "<p>Thanks for your order.</p>", []model.Attachment{pdf,
			// Named in Content-Type only: a text part that is attached.
			{Name: "items.csv", Type: "text/csv", Size: 13, SHA256: "9a3bc98e874391edfb0afa1ce132d62839884971d8cd44b3eb787f9741ec21c9"}}},
		{"rfc2231.eml", "Anbei die Rechnung.", "", []model.Attachment{
			{Name: "Rechnung Nr. 42 Müller.pdf", Type: "application/pdf", Size: 77, SHA256: pdfSHA}}},
		{"alternative.eml", "Just text.", "<p>Just text.</p>", nil},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			raw, err := os.ReadFile("testdata/" + tt.file)
			if err != nil {
				t.Fatal(err)
			}
			m, err := ParseMIME(raw)
			if err != nil {
				t.Fatal(err)
			}
			if trim(m.Text) != tt.text || trim(m.HTML) != tt.html {
				t.Errorf("text %q html %q", m.Text, m.HTML)
			}
			if len(m.Attachments) != len(tt.files) {
				t.Fatalf("attachments %+v, want %+v", m.Attachments, tt.files)
			}
			for i, want := range tt.files {
				if got := m.Attachments[i]; got != want {
					t.Errorf("attachment %d = %+v, want %+v", i, got, want)
				}
			}
		})
	}
}

func trim(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

func TestCaptureKeepsAttachments(t *testing.T) {
	s := NewServer(NewInbox(10), Config{})
	srv := httptest.NewServer(s)
	defer srv.Close()
	raw, _ := os.ReadFile("testdata/pdf.eml")
	d := post(t, srv, wire.Capture{Version: 1, Kind: "email", Source: wire.Source{Via: "laravel"}, Email: &wire.Email{Raw: string(raw)}})
	m := getMessage(t, srv, d.ID)
	if len(m.Attachments) != 1 || m.Attachments[0].Name != "invoice-42.pdf" || m.Attachments[0].SHA256 != pdfSHA {
		t.Errorf("attachments %+v", m.Attachments)
	}
	// Today's WordPress connector names its files in a header and sends
	// no MIME: the names are kept.
	d = post(t, srv, wire.Capture{Version: 1, Kind: "email", Source: wire.Source{Via: "wordpress"},
		Email: &wire.Email{To: []string{"a@example.com"}, Subject: "CV", Text: "Attached.",
			Headers: map[string]string{"X-Echo-Attachments": "cv.pdf, photo.JPG"}}})
	m = getMessage(t, srv, d.ID)
	want := []model.Attachment{{Name: "cv.pdf", Type: "application/pdf"}, {Name: "photo.JPG", Type: "image/jpeg"}}
	if len(m.Attachments) != 2 || m.Attachments[0] != want[0] || m.Attachments[1] != want[1] {
		t.Errorf("from the header: %+v", m.Attachments)
	}
	// Without files there's no attachments key at all.
	d = post(t, srv, wire.Capture{Version: 1, Kind: "email", Source: wire.Source{Via: "node"},
		Email: &wire.Email{To: []string{"a@example.com"}, Subject: "Hi", Text: "Hello."}})
	if m = getMessage(t, srv, d.ID); m.Attachments != nil {
		t.Errorf("no files: %+v", m.Attachments)
	}
}

func TestMailpitListsAttachments(t *testing.T) {
	s := NewServer(NewInbox(10), Config{})
	srv := httptest.NewServer(s)
	defer srv.Close()
	ids := map[string]string{}
	for _, f := range []string{"pdf.eml", "inline.eml", "alternative.eml"} {
		raw, _ := os.ReadFile("testdata/" + f)
		ids[f] = post(t, srv, wire.Capture{Version: 1, Kind: "email", Source: wire.Source{Via: "smtp"}, Email: &wire.Email{Raw: string(raw)}}).ID
	}
	var list mailpitList
	mailpitGet(t, srv, "/api/v1/messages", &list)
	count := map[string]int{}
	for _, m := range list.Messages {
		count[m.ID] = m.Attachments
	}
	if count[ids["pdf.eml"]] != 1 || count[ids["inline.eml"]] != 0 || count[ids["alternative.eml"]] != 0 {
		t.Errorf("summary counts %v for %v", count, ids)
	}
	var pdf mailpitMessage
	mailpitGet(t, srv, "/api/v1/message/"+ids["pdf.eml"], &pdf)
	want := mailpitAttachment{FileName: "invoice-42.pdf", ContentType: "application/pdf", Size: 77, Checksums: mailpitChecksums{SHA256: pdfSHA}}
	if len(pdf.Attachments) != 1 || pdf.Attachments[0] != want || len(pdf.Inline) != 0 {
		t.Errorf("pdf message: %+v %+v", pdf.Attachments, pdf.Inline)
	}
	var inline mailpitMessage
	mailpitGet(t, srv, "/api/v1/message/"+ids["inline.eml"], &inline)
	if len(inline.Inline) != 2 || inline.Inline[1].FileName != "banner.png" || len(inline.Attachments) != 0 {
		t.Errorf("inline message: %+v %+v", inline.Inline, inline.Attachments)
	}
	var plain map[string]any
	mailpitGet(t, srv, "/api/v1/message/"+ids["alternative.eml"], &plain)
	if a, ok := plain["Attachments"].([]any); !ok || len(a) != 0 {
		t.Errorf("no files: Attachments = %v, want []", plain["Attachments"])
	}
}
