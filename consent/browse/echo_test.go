// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browse

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/echo/consent"
)

// TestEchoRunHeaderSameSiteOnly: with --echo every same-site request
// carries the run's X-Echo-Run, replacing one the page set; a third
// party never gets it. -H headers go along as before.
func TestEchoRunHeaderSameSiteOnly(t *testing.T) {
	r := unitRecorder(t, consent.RunReject, nil)
	r.echoRun = "2026-10-26T09-20-41-c4d5e6/page-1-reject"
	var have proto.NetworkHeaders
	if err := json.Unmarshal([]byte(`{"x-echo-run":"forged/by-the-page","Accept":"*/*"}`), &have); err != nil {
		t.Fatal(err)
	}
	same, _ := url.Parse("https://shop.test/api")
	third, _ := url.Parse("https://connect.facebook.test/tr")
	got := r.sameSiteHeaders(same, have)
	runs := 0
	for _, h := range got {
		if strings.EqualFold(h.Name, "X-Echo-Run") {
			runs++
			if h.Value != r.echoRun {
				t.Errorf("X-Echo-Run = %q", h.Value)
			}
		}
	}
	if runs != 1 || len(got) != 2 {
		t.Errorf("same-site headers %+v", got)
	}
	if got := r.sameSiteHeaders(third, have); got != nil {
		t.Errorf("a third party got %+v", got)
	}
	if r.plan.Headers != nil {
		t.Errorf("the plan's headers changed: %v", r.plan.Headers)
	}
}

// TestEchoNames: actions are named after the evidence files; Echo's
// ga4 provider is the Measurement Protocol.
func TestEchoNames(t *testing.T) {
	if got := echoAction(2, consent.RunWithdraw); got != "page-2-withdraw" {
		t.Errorf("echoAction = %q", got)
	}
	for in, want := range map[string]string{"ga4": "ga4-mp", "meta-capi": "meta-capi", "x\u202ey": `x\u202ey`} {
		if got := echoProvider(in); got != want {
			t.Errorf("echoProvider(%q) = %q; want %q", in, got, want)
		}
	}
	if newEchoLink(nil) != nil {
		t.Error("a link without --echo")
	}
	l := newEchoLink(&consent.EchoLink{URL: "http://user:pw@127.0.0.1:8025/", Token: "t"})
	if strings.Contains(l.url, "pw") {
		t.Errorf("the note would show the login: %s", l.url)
	}
}
