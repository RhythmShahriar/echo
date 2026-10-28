// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browse

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/rhythm/kit/httpx"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/browse/egress"
)

// maxRedirects is the most redirects a start page may take: one more
// is a loop as far as a check is concerned (PRD 13.6).
const maxRedirects = 10

// maxDocHops bounds the hops a run keeps of one document load; a loop
// is stopped long before it.
const maxDocHops = 64

// docWait is how long a navigation that returned waits for Network to
// report its document's status.
const docWait = 2 * time.Second

// docNav is the top frame's latest document load: every URL it went
// through (redirects), the status and headers of the last hop or
// Chrome's error.
type docNav struct {
	gen     int // counts top-frame document loads
	id      proto.NetworkRequestID
	hops    []string // raw URLs in order; the first is the one asked for
	nhops   int      // hops seen, kept or not
	status  int
	headers map[string]string // lower-case names
	errText string
}

// docRequestLocked notes a top-frame document request: a redirect adds
// a hop to the load it belongs to, anything else starts a new load.
func (r *recorder) docRequestLocked(e *proto.NetworkRequestWillBeSent) {
	if e.RedirectResponse != nil && e.RequestID == r.doc.id {
		r.doc.nhops++
		if len(r.doc.hops) < maxDocHops {
			r.doc.hops = append(r.doc.hops, e.Request.URL)
		}
		return
	}
	r.doc = docNav{gen: r.doc.gen + 1, id: e.RequestID, hops: []string{e.Request.URL}, nhops: 1}
}

// docResponse notes the status and headers of the current document
// load's last hop.
func (r *recorder) docResponse(id proto.NetworkRequestID, resp *proto.NetworkResponse) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id != r.doc.id || r.doc.id == "" {
		return
	}
	r.doc.status = resp.Status
	r.doc.headers = make(map[string]string, len(resp.Headers))
	for k, v := range resp.Headers {
		r.doc.headers[strings.ToLower(k)] = v.Str()
	}
}

// docFailed notes Chrome's error for the current document load.
func (r *recorder) docFailed(id proto.NetworkRequestID, errText string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id == r.doc.id && r.doc.id != "" {
		r.doc.errText = errText
	}
}

// docState returns a copy of the current document load.
func (r *recorder) docState() docNav {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.doc
	d.hops = append([]string(nil), d.hops...)
	return d
}

// looping reports whether a load went round in circles: more than
// maxRedirects redirects, or one URL a third time. A URL seen twice is
// common (a cookie check that sends the visitor back) and fine.
func (d docNav) looping() bool {
	if d.nhops-1 > maxRedirects {
		return true
	}
	seen := map[string]int{}
	for _, h := range d.hops {
		seen[h]++
		if seen[h] >= 3 {
			return true
		}
	}
	return false
}

// navigateCtx loads raw in the tab; ctx bounds the wait for Chrome's
// answer, which comes once the document commits or fails.
func (s *session) navigateCtx(ctx context.Context, raw string) error {
	s.rec.navigating()
	c := s.main
	c.ctx = ctx
	res, err := proto.PageNavigate{URL: raw}.Call(c)
	if err != nil {
		return err
	}
	if res.ErrorText != "" {
		return errors.New(res.ErrorText)
	}
	return nil
}

// loaded is what a navigation ended on.
type loaded struct {
	doc   docNav
	final string // raw URL of the last hop
	err   string // Chrome's error, "" when the document loaded
}

// load navigates the run's tab to raw and waits for it to settle. A
// load going round in circles is stopped and comes back with doc
// looping. The error is the context's only: what Chrome said about the
// page is in loaded.
func (a *attempt) load(ctx context.Context, raw string) (loaded, error) {
	s := a.run.s
	rec := s.rec
	before := rec.docState().gen
	loads := rec.loads.Load()
	rec.activity.Store(time.Now().UnixNano())
	done := make(chan error, 1)
	go func() { done <- s.navigateCtx(ctx, raw) }()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	var navErr error
wait:
	for {
		select {
		case navErr = <-done:
			break wait
		case <-ctx.Done():
			return loaded{}, ctx.Err()
		case <-tick.C:
			if d := rec.docState(); d.gen > before && d.looping() {
				stop := s.main
				stop.ctx = ctx
				_ = proto.PageStopLoading{}.Call(stop)
				return loaded{doc: d, final: last(d.hops)}, nil
			}
		}
	}
	if ctx.Err() != nil {
		return loaded{}, ctx.Err()
	}
	out := loaded{}
	if navErr != nil {
		out.err = navErr.Error()
	}
	// Network's report of the document trails Page.navigate's answer.
	deadline := time.Now().Add(docWait)
	for {
		d := rec.docState()
		if d.gen > before && (d.status != 0 || d.errText != "") || time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return loaded{}, ctx.Err()
		case <-tick.C:
		}
	}
	out.doc = rec.docState()
	if out.doc.gen > before {
		out.final = last(out.doc.hops)
	}
	if out.err == "" && out.doc.errText != "" && out.doc.status == 0 {
		out.err = out.doc.errText
	}
	if out.err == "" {
		if err := s.settleAfter(ctx, loads); err != nil {
			return out, err
		}
		// A redirect loop Chrome itself gave up on still shows here.
		out.doc = rec.docState()
	}
	return out, nil
}

// last is the last element of s, or "".
func last(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[len(s)-1]
}

// pageFacts are what a start page's checks need beyond its document:
// its title and the scripts and frames it loads.
type pageFacts struct {
	Title string   `json:"title"`
	Srcs  []string `json:"srcs"`
}

// pageFactsJS lists a page's title and the first 200 script and frame
// sources, in Echo's isolated world.
const pageFactsJS = `() => ({title: String(document.title || "").slice(0, 200),
srcs: Array.from(document.querySelectorAll("script[src], iframe[src]"), (e) => String(e.src).slice(0, 2048)).slice(0, 200)})`

// startFatal checks the start page's load as PRD 10.2 sorts the ways a
// site can't be checked: a loop, an unreachable site, a certificate
// this machine doesn't trust, a redirect to another host, a bot check
// and a login wall. nil means the page can be checked.
func (a *attempt) startFatal(ctx context.Context, start *url.URL, l loaded) *consent.FatalError {
	show := func(raw string) string { return consent.MaskURL(raw, a.plan.ShowIDs) }
	startShown := show(start.String())
	if why := a.refusal(start); why != "" {
		// Echo's own guard answered, not the site: its 403 or Chrome's
		// ERR_BLOCKED_BY_CLIENT would read as the site's failure.
		return &consent.FatalError{Code: consent.FatalRefused, URL: startShown, Detail: why}
	}
	switch {
	case l.doc.looping() || strings.Contains(l.err, "ERR_TOO_MANY_REDIRECTS"):
		return &consent.FatalError{Code: consent.FatalRedirectLoop, URL: startShown,
			Detail: redirectDetail(l.doc, show)}
	case l.err != "":
		return a.loadError(start, startShown, l.err)
	}
	if l.final != "" {
		if fu, err := url.Parse(l.final); err == nil && !a.plan.FollowHost && !httpx.SameSite(start, fu) {
			return &consent.FatalError{Code: consent.FatalRedirectHost, URL: startShown, To: show(l.final)}
		}
	}
	if l.doc.status == 502 {
		if d := a.dialError(l.final, start); d != "" {
			return &consent.FatalError{Code: consent.FatalUnreachable, URL: startShown, Detail: d}
		}
	}
	var facts pageFacts
	if raw, err := a.tab.EvalIsolated(ctx, pageFactsJS); err == nil {
		_ = json.Unmarshal(raw, &facts)
	}
	if wall := botWall(l.doc.status, l.doc.headers, facts.Title, facts.Srcs); wall != "" {
		return &consent.FatalError{Code: consent.FatalBotWall, URL: startShown, Status: l.doc.status, Detail: wall}
	}
	if l.doc.status == 401 || l.doc.status == 403 {
		return &consent.FatalError{Code: consent.FatalHTTPStatus, URL: startShown, Status: l.doc.status}
	}
	return nil
}

// refusal says why Echo itself never loads start, or "" when it may:
// a cloud metadata address, or a host --map-only refuses because no
// --map-host covers it.
func (a *attempt) refusal(start *url.URL) string {
	host := start.Hostname()
	switch {
	case egress.IsMetadata(host):
		return consent.RefusedMetadata
	case a.plan.MapOnly && !egress.MatchHost(mapKeys(a.plan.MapHosts), host):
		return consent.RefusedNotMapped
	}
	return ""
}

// redirectDetail names how many redirects a looping load took and the
// last URL it reached, masked.
func redirectDetail(d docNav, show func(string) string) string {
	n := max(d.nhops-1, 0)
	s := "more than 10 redirects"
	if n <= maxRedirects {
		s = "a redirect loop"
	}
	if l := last(d.hops); l != "" {
		s += ", last to " + show(l)
	}
	return s
}

// loadError sorts Chrome's error for the start page: a certificate it
// doesn't trust, or a site that didn't answer. The guard's own reason
// for a failed connection beats Chrome's word for it.
func (a *attempt) loadError(start *url.URL, shown, errText string) *consent.FatalError {
	code := consent.FatalUnreachable
	if strings.Contains(errText, "ERR_CERT_") || strings.Contains(errText, "ERR_SSL_") || strings.Contains(errText, "ERR_BAD_SSL") {
		code = consent.FatalTLS
	}
	detail := consent.MaskURL(errText, false)
	if me, ok := a.mapErr(start); ok {
		// --map-host answered: Chrome only saw Echo fail the request, so
		// its own word for it says nothing.
		if me.tls {
			code = consent.FatalTLS
		}
		return &consent.FatalError{Code: code, URL: shown, Detail: mapErrorText(me)}
	}
	if code == consent.FatalUnreachable {
		if d := a.dialError(start.String(), start); d != "" {
			detail = d
		}
	}
	return &consent.FatalError{Code: code, URL: shown, Detail: detail}
}

// mapErr is why the mapped request for start failed, when --map-host
// answers its host and it did.
func (a *attempt) mapErr(start *url.URL) (mapError, bool) {
	if a.run == nil || a.run.s == nil || a.run.s.rec == nil {
		return mapError{}, false
	}
	return a.run.s.rec.mapErr(start.Hostname())
}

// mapErrorText says why a mapped request failed, naming the mapped
// address: "https://127.0.0.1:39102 (--map-host): x509: certificate
// signed by unknown authority".
func mapErrorText(me mapError) string {
	msg := me.err.Error()
	var ue *url.Error
	if errors.As(me.err, &ue) {
		msg = ue.Err.Error()
	}
	return consent.SafeText(consent.MaskURL(me.addr, false)+" (--map-host): "+msg, 300)
}

// dialError is why the target's guard couldn't connect to raw's host,
// or "".
func (a *attempt) dialError(raw string, start *url.URL) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		u = start
	}
	g, err := a.e.guard(a.target)
	if err != nil {
		return ""
	}
	port := u.Port()
	if port == "" {
		port = "443"
		if u.Scheme == "http" {
			port = "80"
		}
	}
	return g.DialError(u.Hostname(), port)
}

// botWall names the bot check a start page showed instead of itself:
// "Cloudflare", "Akamai", "DataDome", "PerimeterX", or "" when it shows
// none. Script hosts alone never count on a page that answered 2xx:
// the same vendors' scripts run on ordinary pages too.
func botWall(status int, headers map[string]string, title string, srcs []string) string {
	h := func(name string) string { return strings.ToLower(headers[name]) }
	t := strings.ToLower(strings.TrimSpace(title))
	blocked := status == 403 || status == 429 || status == 503
	switch {
	case h("cf-mitigated") == "challenge":
		return "Cloudflare"
	case t == "just a moment..." || t == "attention required! | cloudflare" || (blocked && strings.Contains(h("server"), "cloudflare") && strings.Contains(t, "cloudflare")):
		return "Cloudflare"
	case blocked && h("x-datadome") != "":
		return "DataDome"
	case t == "access to this page has been denied" || (blocked && strings.HasPrefix(t, "access to this page has been denied")):
		return "PerimeterX"
	case blocked && strings.Contains(h("server"), "akamaighost") && strings.Contains(t, "access denied"):
		return "Akamai"
	}
	if !blocked {
		return ""
	}
	for _, src := range srcs {
		u, err := url.Parse(src)
		if err != nil {
			continue
		}
		host, path := strings.ToLower(u.Hostname()), u.Path
		switch {
		case host == "challenges.cloudflare.com" || strings.HasPrefix(path, "/cdn-cgi/challenge-platform/"):
			return "Cloudflare"
		case host == "captcha-delivery.com" || strings.HasSuffix(host, ".captcha-delivery.com"):
			return "DataDome"
		case host == "captcha.px-cdn.net" || strings.HasSuffix(host, ".perimeterx.net") || strings.HasSuffix(host, ".px-cloud.net"):
			return "PerimeterX"
		case strings.HasPrefix(path, "/_sec/cp_challenge/"):
			return "Akamai"
		}
	}
	return ""
}
