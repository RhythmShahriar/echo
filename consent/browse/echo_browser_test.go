// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build browser

package browse

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/model"
)

// fakeEcho is Echo's API on 127.0.0.1 as --echo uses it: actions begin
// and end; the analytics messages it caught are listed by run and
// action. A test files a message as a site's connector would.
type fakeEcho struct {
	srv *httptest.Server

	mu     sync.Mutex
	begins []string // run/action
	ends   []string // leases
	msgs   []model.Message
}

func newFakeEcho(t *testing.T) *fakeEcho {
	t.Helper()
	f := &fakeEcho{}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeEcho) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/actions/begin":
		var b struct{ Run, Action string }
		_ = json.NewDecoder(r.Body).Decode(&b)
		f.begins = append(f.begins, b.Run+"/"+b.Action)
		_ = json.NewEncoder(w).Encode(map[string]string{"lease": fmt.Sprintf("lease-%d", len(f.begins))})
	case r.Method == http.MethodPost && r.URL.Path == "/v1/actions/end":
		var b struct{ Lease string }
		_ = json.NewDecoder(r.Body).Decode(&b)
		f.ends = append(f.ends, b.Lease)
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/messages":
		q := r.URL.Query()
		out := []model.Message{}
		for _, m := range f.msgs {
			if m.Run == q.Get("run") && m.Action == q.Get("action") && string(m.Channel) == q.Get("channel") {
				out = append(out, m)
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	default:
		http.NotFound(w, r)
	}
}

// file records a message a connector caught for the X-Echo-Run value.
func (f *fakeEcho) file(runValue, provider, event string, ch model.Channel) {
	run, action, _ := strings.Cut(runValue, "/")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.msgs = append(f.msgs, model.Message{ID: fmt.Sprintf("m%d", len(f.msgs)+1), Run: run, Action: action, Channel: ch,
		Provider: provider, Event: event, Status: model.Faked, At: time.Now()})
}

// echoShop serves the shop on shop.test and a third-party pixel host,
// keeping the X-Echo-Run of every request by host and path. Its server
// "sends" a GA4 Measurement Protocol page_view for every page and a
// Meta Conversions API event when /capi is asked for, as a site with an
// Echo connector would.
func echoShop(t *testing.T, echo *fakeEcho) (string, func(hostPath string) []string) {
	t.Helper()
	var mu sync.Mutex
	seen := map[string][]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		run := r.Header.Get("X-Echo-Run")
		mu.Lock()
		seen[host+r.URL.Path] = append(seen[host+r.URL.Path], run)
		mu.Unlock()
		switch {
		case host != "shop.test":
			w.Header().Set("Content-Type", "image/gif")
			_, _ = w.Write(heldGIF)
		case r.URL.Path == "/capi":
			if run != "" {
				echo.file(run, "meta-capi", "Lead", model.Analytics)
			}
			_, _ = fmt.Fprint(w, "ok")
		default:
			if run != "" && r.URL.Path == "/" {
				echo.file(run, "ga4", "page_view", model.Analytics)
				echo.file(run, "sendgrid", "", model.Email)
			}
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, shopPage(`<img src="https://pixel.other.test/p.gif">
<script>document.getElementById("reject").addEventListener("click", () => fetch("/capi"));</script>`))
		}
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://"), func(hostPath string) []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(seen[hostPath])
	}
}

// echoPlan is a reject run of the echo shop with --echo at url.
func echoPlan(t *testing.T, addr, url string) *consent.Plan {
	t.Helper()
	p := planFor(t, addr, consent.RunReject)
	p.Pages = 0
	p.Echo = &consent.EchoLink{URL: url, Token: "t"}
	return p
}

// TestEchoTagSameSiteOnly: every same-site request of a run carries the
// run's X-Echo-Run; the third-party pixel never does.
func TestEchoTagSameSiteOnly(t *testing.T) {
	echo := newFakeEcho(t)
	addr, seen := echoShop(t, echo)
	p := echoPlan(t, addr, echo.srv.URL)
	if _, err := runCheck(t, p, &pageCMP{}); err != nil {
		t.Fatal(err)
	}
	want := p.ID + "/page-1-reject"
	for _, path := range []string{"shop.test/", "shop.test/capi"} {
		got := seen(path)
		if len(got) == 0 || slices.ContainsFunc(got, func(v string) bool { return v != want }) {
			t.Errorf("%s: X-Echo-Run %q; want %q", path, got, want)
		}
	}
	pixel := seen("pixel.other.test/p.gif")
	if len(pixel) == 0 || slices.ContainsFunc(pixel, func(v string) bool { return v != "" }) {
		t.Errorf("the third party saw X-Echo-Run %q", pixel)
	}
	echo.mu.Lock()
	defer echo.mu.Unlock()
	if !slices.Equal(echo.begins, []string{want}) || !slices.Equal(echo.ends, []string{"lease-1"}) {
		t.Errorf("begins %v, ends %v", echo.begins, echo.ends)
	}
}

// TestEchoMessagesMapped: the analytics messages Echo caught for the run
// are listed with their provider and phase: a Meta Conversions API
// event after the Reject click is after-reject; the GA4 page_view of
// the first load came before. Mail is not analytics and is left out.
func TestEchoMessagesMapped(t *testing.T) {
	echo := newFakeEcho(t)
	addr, _ := echoShop(t, echo)
	res, err := runCheck(t, echoPlan(t, addr, echo.srv.URL), &pageCMP{})
	if err != nil {
		t.Fatal(err)
	}
	r := res.Pages[0].Runs[0]
	if r.Choice == nil {
		t.Fatalf("no choice: %+v", r)
	}
	var capi, ga4 []consent.EchoMessage
	for _, m := range r.Server {
		switch m.Provider {
		case "meta-capi":
			capi = append(capi, m)
		case "ga4-mp":
			ga4 = append(ga4, m)
		default:
			t.Errorf("unexpected message %+v", m)
		}
	}
	if len(capi) != 1 || capi[0].Phase != consent.PhaseAfterReject || capi[0].Event != "Lead" || capi[0].AtMS < r.Choice.AtMS || capi[0].ID == "" {
		t.Errorf("capi %+v (choice at %d)", capi, r.Choice.AtMS)
	}
	if len(ga4) == 0 || ga4[0].Phase != consent.PhaseBefore || ga4[0].Event != "page_view" {
		t.Errorf("ga4 %+v", ga4)
	}
	// The shop is https answered from plain http: that is the only note.
	if !slices.Equal(res.Notes, []string{NoteMappedHTTP}) {
		t.Errorf("notes %q", res.Notes)
	}
}

// TestEchoDownIsANote: an Echo that doesn't answer is one note on the
// result; the runs go on and check what they can.
func TestEchoDownIsANote(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	down := "http://" + ln.Addr().String()
	_ = ln.Close()
	echo := newFakeEcho(t)
	addr, seen := echoShop(t, echo)
	p := echoPlan(t, addr, down)
	p.Runs = []consent.RunKind{consent.RunNone, consent.RunReject}
	res, err := runCheck(t, p, &pageCMP{})
	if err != nil {
		t.Fatal(err)
	}
	want := "Echo at " + down + " didn't answer; server-side tracking wasn't checked."
	if !slices.Equal(res.Notes, []string{want, NoteMappedHTTP}) {
		t.Errorf("notes %q; want %q", res.Notes, want)
	}
	for _, r := range res.Pages[0].Runs {
		if r.Status != consent.StatusOK || len(r.Server) != 0 {
			t.Errorf("%s: %s, server %+v", r.Kind, r.Status, r.Server)
		}
	}
	if got := seen("shop.test/"); slices.ContainsFunc(got, func(v string) bool { return v != "" }) {
		t.Errorf("X-Echo-Run sent without Echo: %q", got)
	}
}
