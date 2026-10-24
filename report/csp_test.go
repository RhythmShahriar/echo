// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"html"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/model"
)

const tracker = `<p>Hi</p><img src="https://track.example/open.gif?u=42" width="1" height="1">`

func trackerReport(t *testing.T, body string) string {
	t.Helper()
	at := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	r := &model.Run{Schema: model.Schema, Version: "1.0.0", Mode: "seal", Started: at, Actions: []model.Action{{ID: "a", Label: "Order",
		Messages: []model.Message{{ID: "m1", Channel: model.Email, Status: model.Faked, To: []string{"a@shop.test"}, Subject: "Hi", HTML: body, At: at}}}}}
	var buf bytes.Buffer
	if err := HTML(&buf, Result{Run: r}); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestReportCSP(t *testing.T) {
	out := trackerReport(t, tracker)
	meta := regexp.MustCompile(`<meta http-equiv="Content-Security-Policy" content="([^"]+)">`).FindStringSubmatch(out)
	if meta == nil {
		t.Fatalf("no CSP meta in the report")
	}
	csp := html.UnescapeString(meta[1])
	for _, want := range []string{"default-src 'none'", "style-src 'unsafe-inline'", "frame-src 'self'", "base-uri 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}
	// One inline script, and the policy allows exactly it.
	scripts := regexp.MustCompile(`(?s)<script>(.*?)</script>`).FindAllStringSubmatch(out, -1)
	if len(scripts) != 1 {
		t.Fatalf("want one inline script, got %d", len(scripts))
	}
	sum := sha256.Sum256([]byte(scripts[0][1]))
	if want := "script-src 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"; !strings.Contains(csp, want) {
		t.Errorf("CSP %q doesn't allow the rendered script (%s)", csp, want)
	}
	// The script's two email policies are the ones html.go writes.
	for _, p := range []string{emailBlocked, emailAllowed} {
		if !strings.Contains(scripts[0][1], `"`+p+`"`) {
			t.Errorf("the script lacks the email policy %q", p)
		}
	}
}

func TestTrackerStaysInSandbox(t *testing.T) {
	out := trackerReport(t, tracker)
	frame := regexp.MustCompile(`<iframe sandbox title="Email preview" srcdoc="([^"]*)"></iframe>`).FindStringSubmatch(out)
	if frame == nil {
		t.Fatalf("no sandboxed srcdoc iframe:\n%s", out)
	}
	doc := html.UnescapeString(frame[1])
	if !strings.HasPrefix(doc, `<meta http-equiv="Content-Security-Policy" content="`+emailBlocked+`">`) {
		t.Errorf("the preview doesn't start with the blocking policy: %q", doc)
	}
	if !strings.Contains(doc, `src="https://track.example/open.gif?u=42"`) {
		t.Errorf("the email itself is kept: %q", doc)
	}
	// The tracker is only ever inside the srcdoc, never live in the page.
	if strings.Count(out, "track.example") != 1 {
		t.Errorf("the tracker URL appears outside the preview")
	}
	if !strings.Contains(out, `<button class="remote" type="button" data-remote>Load remote images</button><iframe`) {
		t.Errorf("no remote images button before the preview")
	}
}

func TestNoRemoteButtonWithoutRemoteContent(t *testing.T) {
	out := trackerReport(t, `<p>Hi</p><img src="data:image/png;base64,iVBORw0KGgo=">`)
	if strings.Contains(out, "data-remote>") {
		t.Error("an email with only inline images needs no button")
	}
	if !strings.Contains(out, "<iframe sandbox") {
		t.Error("the preview is still shown")
	}
}

func TestRemoteRef(t *testing.T) {
	for s, want := range map[string]bool{
		`<img src="https://t.example/p.gif">`:                  true,
		`<img SRC = '//cdn.example/x.png'>`:                    true,
		`<td background="http://x.example/bg.jpg">`:            true,
		`<div style="background:url( 'https://x.example/a')">`: true,
		`<img srcset="https://x.example/a 2x">`:                true,
		`<a href="https://shop.test/order/1">Order</a>`:        false,
		`<img src="data:image/png;base64,AAAA">`:               false,
		`<p>Visit https://shop.test</p>`:                       false,
		`<img src="cid:logo">`:                                 false,
	} {
		if got := remoteRef.MatchString(s); got != want {
			t.Errorf("remoteRef(%q) = %v, want %v", s, got, want)
		}
	}
}
