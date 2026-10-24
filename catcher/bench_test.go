// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package catcher

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/mask"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/wire"
)

// largeText is n bytes of ordinary email text.
func largeText(n int) string {
	line := "Hello Jane, your order #42 has shipped and is on its way to you.\n"
	return strings.Repeat(line, n/len(line)+1)[:n]
}

// pdfEmail is a raw message with a short text part and a base64 PDF
// attachment of about n bytes.
func pdfEmail(n int) string {
	pdf := make([]byte, n*3/4)
	for i := range pdf {
		pdf[i] = byte(i * 7)
	}
	enc := base64.StdEncoding.EncodeToString(pdf)
	var b strings.Builder
	for i := 0; i < len(enc); i += 76 {
		b.WriteString(enc[i:min(i+76, len(enc))] + "\r\n")
	}
	return "From: shop@shop.test\r\nTo: jane@example.com\r\nSubject: Invoice 42\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=b\r\n\r\n--b\r\nContent-Type: text/plain\r\n\r\nYour invoice is attached.\r\n" +
		"--b\r\nContent-Type: application/pdf; name=invoice-42.pdf\r\nContent-Disposition: attachment; filename=invoice-42.pdf\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" + b.String() + "--b--\r\n"
}

func BenchmarkCaptureLargeEmail(b *testing.B) {
	for _, c := range []struct {
		name  string
		email wire.Email
	}{
		{"1MiB text", wire.Email{To: []string{"jane@example.com"}, Subject: "Order", Text: largeText(1 << 20)}},
		{"2MiB MIME with a PDF", wire.Email{Raw: pdfEmail(2 << 20)}},
	} {
		b.Run(c.name, func(b *testing.B) {
			s := NewServer(NewInbox(10), Config{})
			b.SetBytes(int64(len(c.email.Text) + len(c.email.Raw)))
			for b.Loop() {
				e := c.email
				if _, err := s.Capture(wire.Capture{Version: 1, Kind: "email", Source: wire.Source{Via: "laravel"}, Email: &e}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// TestLargeCaptureIsCutBeforeMasking: a 20 MiB text took 29 s to mask in
// full. Cut first, it's quick and what's stored fits MaxBody.
func TestLargeCaptureIsCutBeforeMasking(t *testing.T) {
	s := NewServer(NewInbox(10), Config{})
	big := largeText(20 << 20)
	start := time.Now()
	d, err := s.Capture(wire.Capture{Version: 1, Kind: "email", Email: &wire.Email{To: []string{"jane@example.com"}, Text: big}})
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 500*time.Millisecond*slowdown {
		t.Errorf("a 20 MiB text capture took %s", took)
	}
	if m, _ := s.Inbox.Get(d.ID); !m.Truncated || len(m.Text) > 256<<10 {
		t.Errorf("20 MiB text: truncated %v, %d bytes stored", m.Truncated, len(m.Text))
	}

	// Every field is cut: the subject and each address too. A smaller cap
	// keeps this quick under the race detector.
	s = NewServer(NewInbox(10), Config{MaxBody: 16 << 10})
	d, err = s.Capture(wire.Capture{Version: 1, Kind: "email", Email: &wire.Email{
		To: []string{"jane@example.com", big}, Subject: big, Text: big, HTML: "<p>" + big}})
	if err != nil {
		t.Fatal(err)
	}
	m, _ := s.Inbox.Get(d.ID)
	if !m.Truncated {
		t.Error("Truncated not set")
	}
	limit := 16 << 10
	for name, v := range map[string]string{"text": m.Text, "html": m.HTML, "subject": m.Subject, "to[1]": m.To[1]} {
		if len(v) > limit || !strings.HasSuffix(v, truncatedMark) {
			t.Errorf("%s: %d bytes stored, want at most %d and the mark", name, len(v), limit)
		}
	}
	if m.To[0] != "jane@example.com" {
		t.Errorf("a short address was changed: %q", m.To[0])
	}

	// A large MIME message: the PDF is named, not stored.
	d, err = s.Capture(wire.Capture{Version: 1, Kind: "email", Email: &wire.Email{Raw: pdfEmail(2 << 20)}})
	if err != nil {
		t.Fatal(err)
	}
	m, _ = s.Inbox.Get(d.ID)
	if !m.Truncated || len(m.Body) > limit || strings.TrimSpace(m.Text) != "Your invoice is attached." || m.Subject != "Invoice 42" {
		t.Errorf("MIME: truncated %v, body %d bytes, text %q", m.Truncated, len(m.Body), m.Text)
	}

	// Small messages are left alone.
	d, _ = s.Capture(wire.Capture{Version: 1, Kind: "email", Email: &wire.Email{To: []string{"a@example.com"}, Text: "hi"}})
	if m, _ := s.Inbox.Get(d.ID); m.Truncated || m.Text != "hi" {
		t.Errorf("small message: %+v", m)
	}
}

func TestCutKeepsWholeCharacters(t *testing.T) {
	// 26 bytes leave 11 for the text, which ends inside a character.
	s := NewServer(NewInbox(10), Config{MaxBody: 11 + len(truncatedMark)})
	v, cut := s.cutMasked(strings.Repeat("é", 20), nil)
	if !cut || v != "ééééé"+truncatedMark {
		t.Errorf("cut = %q, %v", v, cut)
	}
	// Too small for the mark: the text alone, still whole characters.
	s = NewServer(NewInbox(10), Config{MaxBody: 5})
	if v, cut := s.cutMasked("ééééééé", nil); !cut || v != "éé" {
		t.Errorf("tiny cut = %q, %v", v, cut)
	}
}

func TestCaptureLimit(t *testing.T) {
	srv := httptest.NewServer(NewServer(NewInbox(10), Config{}))
	defer srv.Close()
	for _, c := range []struct {
		size int
		want int
	}{{MaxCapture + 1024, http.StatusRequestEntityTooLarge}, {MaxCapture / 2, http.StatusOK}} {
		body, _ := json.Marshal(wire.Capture{Version: 1, Kind: "email", Email: &wire.Email{To: []string{"a@example.com"}, Text: largeText(c.size)}})
		resp, err := http.Post(srv.URL+"/v1/capture", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != c.want {
			t.Errorf("%d byte capture: %s, want %d", len(body), resp.Status, c.want)
		}
	}
}

// FuzzCutMasking: masking only MaxBody plus the margin stores the same as
// masking everything and then cutting, for secrets more than the margin
// away from the cut.
func FuzzCutMasking(f *testing.F) {
	f.Add(uint64(1), "hello world\n")
	f.Add(uint64(42), "ordinary text with spaces and lines\n\n")
	f.Fuzz(func(t *testing.T, seed uint64, filler string) {
		const maxBody = 16 << 10
		s := NewServer(NewInbox(1), Config{MaxBody: maxBody})
		// Filler from letters, spaces and newlines alone matches no rule,
		// so only the planted secrets are masked.
		fill := []byte(filler)
		for i, c := range fill {
			fill[i] = " \nabcdefghijklmnopqrstuvwxyz"[int(c)%28]
		}
		if len(fill) == 0 {
			fill = []byte("a b")
		}
		rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
		secrets := []string{"password=Zq9TOPSECRETvalue77", "https://shop.test/?key=Zq9TOPSECRETvalue77&page=2",
			"Your login code is 482913", "https://api.telegram.org/bot1234:AAHZq9TOPSECRETvalue77abcdefghij/getMe"}
		var b strings.Builder
		for b.Len() < 3*maxBody {
			pos := b.Len()
			far := pos+100 < maxBody-margin || pos > maxBody+2*margin
			if far && rng.IntN(8) == 0 {
				b.WriteString(" " + secrets[rng.IntN(len(secrets))] + " ")
				continue
			}
			b.Write(fill[:min(len(fill), 1+rng.IntN(64))])
		}
		v := b.String()
		full := mask.Text(v)
		want := full[:runeStart(full, maxBody-len(truncatedMark))] + truncatedMark
		got, cut := s.cutMasked(v, mask.Text)
		if !cut || got != want {
			t.Fatalf("cut first differs from mask first (len %d vs %d)", len(got), len(want))
		}
		if strings.Contains(got, "Zq9TOPSECRET") || strings.Contains(got, "482913") {
			t.Fatal("a secret survived")
		}
	})
}

func TestInboxEvictsByBytes(t *testing.T) {
	in := NewInbox(100)
	in.SetMaxBytes(10 << 10)
	for i := range 5 {
		in.Add(model.Message{ID: string(rune('a' + i)), Text: strings.Repeat("x", 3<<10)})
	}
	got := in.List(Filter{})
	if len(got) != 3 || got[0].ID != "c" || got[2].ID != "e" {
		t.Errorf("kept %d messages: %v", len(got), ids(got))
	}
	if in.Bytes() > 10<<10 {
		t.Errorf("holds %d bytes", in.Bytes())
	}
	// Growing a message by an update evicts too.
	in.Update("e", func(m *model.Message) { m.Response = &model.Response{Body: strings.Repeat("y", 2<<10)} })
	if got := in.List(Filter{}); len(got) != 2 || got[0].ID != "d" {
		t.Errorf("after the update: %v", ids(got))
	}
	// One message bigger than the cap is kept: the newest always is.
	in.Add(model.Message{ID: "huge", Body: strings.Repeat("z", 20<<10)})
	if got := in.List(Filter{}); len(got) != 1 || got[0].ID != "huge" {
		t.Errorf("after a huge message: %v", ids(got))
	}
	// Clearing gives the bytes back.
	in.Clear(Filter{})
	if in.Bytes() != 0 {
		t.Errorf("after clear: %d bytes", in.Bytes())
	}
	in.Add(model.Message{ID: "m1", Channel: model.Email, Text: "abc"})
	in.Delete(model.Email, []string{"m1"})
	if in.Bytes() != 0 {
		t.Errorf("after delete: %d bytes", in.Bytes())
	}
}

func ids(ms []model.Message) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}

// BenchmarkGet shows Get doesn't scan: the time per Get is the same with
// 100 messages as with 10,000.
func BenchmarkGet(b *testing.B) {
	for _, n := range []int{100, 10000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			in := NewInbox(n)
			var first string
			for i := range n {
				m := in.Add(model.Message{Channel: model.Email, Subject: fmt.Sprint(i)})
				if i == 0 {
					first = m.ID // the oldest: the far end of a scan
				}
			}
			for b.Loop() {
				if _, ok := in.Get(first); !ok {
					b.Fatal("not found")
				}
			}
		})
	}
}

func TestErrorsAndNamesAreMaskedAndCut(t *testing.T) {
	s := NewServer(NewInbox(10), Config{MaxBody: 16 << 10})
	bot := "https://api.telegram.org/bot1234:AAHZq9TOPSECRETvalue77abcdefghij/sendMessage"
	long := strings.Repeat("r", 1000)
	d, err := s.Capture(wire.Capture{Version: 1, Kind: "email", Run: long + "/" + long, Error: "post " + bot + ": timeout " + largeText(1<<20),
		Email: &wire.Email{To: []string{"a@example.com"}, Text: "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	m, _ := s.Inbox.Get(d.ID)
	if strings.Contains(m.Error, "TOPSECRET") || len(m.Error) > 16<<10 || !strings.HasPrefix(m.Error, "post ") {
		t.Errorf("error kept raw: %d bytes, %.120q", len(m.Error), m.Error)
	}
	if len(m.Run) != maxName || len(m.Action) != maxName {
		t.Errorf("run %d bytes, action %d, want %d each", len(m.Run), len(m.Action), maxName)
	}
	// A short, clean error is kept as it is.
	d, _ = s.Capture(wire.Capture{Version: 1, Kind: "email", Error: "connection refused", Email: &wire.Email{To: []string{"a@example.com"}}})
	if m, _ := s.Inbox.Get(d.ID); m.Error != "connection refused" || m.Run != "" {
		t.Errorf("clean error: %+v", m)
	}
}
