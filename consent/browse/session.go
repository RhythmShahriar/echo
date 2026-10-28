// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/browse/egress"
	"github.com/RhythmShahriar/echo/internal/browser"
)

// The viewports --viewport names (PRD 7).
const (
	ViewportDesktop = "desktop" // 1366x900 at 1x
	ViewportMobile  = "mobile"  // 390x844 at 3x, touch, a mobile user agent
)

// UASuffix starts the product token every consent run adds to its user
// agent, so a site owner can tell Echo's visits in the logs.
const UASuffix = " rhythm-echo-consent/"

// gpcScript defines navigator.globalPrivacyControl before any page
// script runs, as a browser with Global Privacy Control on does.
const gpcScript = `Object.defineProperty(Navigator.prototype, "globalPrivacyControl", {get: function () { return true; }, configurable: true, enumerable: true});`

// deniedPermissions are the permissions every run's context refuses: a
// banner's click must not grant a page anything a visitor would be
// asked about.
var deniedPermissions = []string{"geolocation", "notifications", "clipboard-read", "clipboard-write", "midi",
	"camera", "microphone", "idle-detection", "storage-access", "top-level-storage-access", "window-management",
	"persistent-storage", "background-sync", "payment-handler"}

// emulation is how every target of a run presents itself: user agent,
// language, time zone, screen and, for the gpc run, Global Privacy
// Control.
type emulation struct {
	ua         string
	acceptLang string
	platform   string
	meta       *proto.EmulationUserAgentMetadata
	locale     string
	timezone   string
	mobile     bool
	width      int
	height     int
	dpr        float64
	gpc        bool
}

// newEmulation works out a run's emulation from the plan and the
// browser's own user agent and version.
func newEmulation(p *consent.Plan, kind consent.RunKind, browserUA, version string) *emulation {
	mobile := strings.EqualFold(strings.TrimSpace(p.Viewport), ViewportMobile)
	e := &emulation{locale: planLocale(p), timezone: planTimezone(p), mobile: mobile, gpc: kind == consent.RunGPC,
		width: 1366, height: 900, dpr: 1}
	if mobile {
		e.width, e.height, e.dpr = 390, 844, 3
	}
	e.acceptLang = acceptLanguage(e.locale)
	e.ua = userAgent(p, browserUA, mobile)
	e.platform, e.meta = platform(version, mobile)
	return e
}

// userAgent is -A as given, else the browser's own with HeadlessChrome
// made Chrome (and the desktop platform made Android's for mobile) plus
// the rhythm-echo-consent product token. Nothing else is disguised.
func userAgent(p *consent.Plan, browserUA string, mobile bool) string {
	if ua := strings.TrimSpace(p.UserAgent); ua != "" {
		return ua
	}
	ua := strings.ReplaceAll(browserUA, "HeadlessChrome", "Chrome")
	if mobile {
		if i, j := strings.Index(ua, "("), strings.Index(ua, ")"); i >= 0 && j > i {
			ua = ua[:i] + "(Linux; Android 10; K)" + ua[j+1:]
		}
		if !strings.Contains(ua, "Mobile Safari/") {
			ua = strings.Replace(ua, "Safari/", "Mobile Safari/", 1)
		}
	}
	v := p.Version
	if v == "" {
		v = "dev"
	}
	return ua + UASuffix + v
}

// platform is navigator.platform and the client hints for this machine
// (or Android for mobile), with Chromium as the only brand so no hint
// says HeadlessChrome either.
func platform(version string, mobile bool) (string, *proto.EmulationUserAgentMetadata) {
	major, _, _ := strings.Cut(version, ".")
	if major == "" {
		major = "0"
	}
	nav, name, arch := "Linux x86_64", "Linux", "x86"
	switch runtime.GOOS {
	case "darwin":
		nav, name = "MacIntel", "macOS"
	case "windows":
		nav, name = "Win32", "Windows"
	}
	if runtime.GOARCH == "arm64" {
		arch = "arm"
	}
	if mobile {
		nav, name, arch = "Linux armv8l", "Android", ""
	}
	full := version
	if full == "" {
		full = major
	}
	return nav, &proto.EmulationUserAgentMetadata{
		Brands:          []*proto.EmulationUserAgentBrandVersion{{Brand: "Chromium", Version: major}, {Brand: "Not_A Brand", Version: "24"}},
		FullVersionList: []*proto.EmulationUserAgentBrandVersion{{Brand: "Chromium", Version: full}, {Brand: "Not_A Brand", Version: "24.0.0.0"}},
		FullVersion:     full,
		Platform:        name,
		Architecture:    arch,
		Mobile:          mobile,
	}
}

// acceptLanguage is the Accept-Language a browser in locale sends:
// de-DE,de;q=0.9 (the tag, then its language).
func acceptLanguage(locale string) string {
	lang, _, ok := strings.Cut(locale, "-")
	if !ok || lang == "" {
		return locale
	}
	return locale + "," + lang + ";q=0.9"
}

// overrideLanguages is the list Chrome's user agent override takes:
// the languages without weights. Chrome adds the weights itself, so
// de-DE,de goes out as de-DE,de;q=0.9.
func overrideLanguages(acceptLang string) string {
	parts := strings.Split(acceptLang, ",")
	for i, p := range parts {
		p, _, _ = strings.Cut(p, ";")
		parts[i] = strings.TrimSpace(p)
	}
	return strings.Join(parts, ",")
}

// apply sets the emulation on one target before it runs. The tab gets
// the screen too; frames take theirs from it.
func (e *emulation) apply(t *target) error {
	s := t.s
	if t.kind == kindWorker {
		// Workers have no Emulation domain; their requests still carry
		// the user agent and, for gpc, the header.
		_ = proto.NetworkSetUserAgentOverride{UserAgent: e.ua, AcceptLanguage: overrideLanguages(e.acceptLang), Platform: e.platform, UserAgentMetadata: e.meta}.Call(s)
		if e.gpc {
			_ = proto.NetworkSetExtraHTTPHeaders{Headers: gpcHeaders()}.Call(s)
		}
		return nil
	}
	if err := (proto.EmulationSetUserAgentOverride{UserAgent: e.ua, AcceptLanguage: overrideLanguages(e.acceptLang), Platform: e.platform, UserAgentMetadata: e.meta}).Call(s); err != nil {
		return fmt.Errorf("setting the user agent: %w", err)
	}
	if err := (proto.EmulationSetLocaleOverride{Locale: e.locale}).Call(s); err != nil && !alreadySet(err) {
		return fmt.Errorf("setting the locale %s: %w", e.locale, err)
	}
	if e.timezone != "" {
		if err := (proto.EmulationSetTimezoneOverride{TimezoneID: e.timezone}).Call(s); err != nil && !alreadySet(err) {
			return fmt.Errorf("setting the time zone %s: %w", e.timezone, err)
		}
	}
	if t.kind == kindPage {
		if err := (proto.EmulationSetDeviceMetricsOverride{Width: e.width, Height: e.height, DeviceScaleFactor: e.dpr, Mobile: e.mobile}).Call(s); err != nil {
			return fmt.Errorf("setting the viewport: %w", err)
		}
		if e.mobile {
			points := 5
			if err := (proto.EmulationSetTouchEmulationEnabled{Enabled: true, MaxTouchPoints: &points}).Call(s); err != nil {
				return fmt.Errorf("turning touch on: %w", err)
			}
		}
	}
	// A file chooser is never shown: the page gets no file.
	_ = proto.PageSetInterceptFileChooserDialog{Enabled: true}.Call(s)
	if e.gpc {
		if err := (proto.NetworkSetExtraHTTPHeaders{Headers: gpcHeaders()}).Call(s); err != nil {
			return fmt.Errorf("sending Sec-GPC: %w", err)
		}
		if _, err := (proto.PageAddScriptToEvaluateOnNewDocument{Source: gpcScript, RunImmediately: true}).Call(s); err != nil {
			return fmt.Errorf("defining navigator.globalPrivacyControl: %w", err)
		}
	}
	return nil
}

// gpcHeaders is the header a browser with Global Privacy Control on
// sends with every request.
func gpcHeaders() proto.NetworkHeaders {
	var h proto.NetworkHeaders
	_ = json.Unmarshal([]byte(`{"Sec-GPC":"1"}`), &h)
	return h
}

// alreadySet is Chrome's answer to an override another session of the
// same target set first; the value is the same, so it is fine.
func alreadySet(err error) bool {
	return strings.Contains(err.Error(), "already in effect")
}

// session is one run's browser context: its own cookies, storage, cache
// and egress guard, one tab and the recorder.
type session struct {
	h    *hub
	ctx  *rod.Browser
	tab  *rod.Page
	rec  *recorder
	main cdp
}

// sessionOptions open one run's context.
type sessionOptions struct {
	Plan    *consent.Plan
	Target  consent.Target
	Kind    consent.RunKind
	Guard   *egress.Proxy
	Env     *emulation
	Timeout time.Duration // how long Chrome may take to open the tab
	Sinks   []func(consent.Event)
	EchoRun string // X-Echo-Run for same-site requests (--echo)
}

// openSession opens a fresh incognito context whose traffic goes
// through the target's guard, denies it downloads and permissions,
// gives it the plan's cookies for the target and opens its tab, set up
// and recording before the first navigation.
func (h *hub) openSession(ctx context.Context, o sessionOptions) (*session, error) {
	bctx, err := h.b.SessionWithProxy(o.Guard.URL(), bypassLoopback)
	if err != nil {
		return nil, fmt.Errorf("consent: opening a browser context: %w", err)
	}
	rec := newRecorder(ctx, h, o.Plan, o.Target.URL, o.Kind, bctx.BrowserContextID, o.Env, o.Guard)
	// Set before the context is routed to the recorder, so no event or
	// request can come first.
	rec.sinks = append(rec.sinks, o.Sinks...)
	rec.echoRun = o.EchoRun
	h.register(bctx.BrowserContextID, rec)
	s := &session{h: h, ctx: bctx, rec: rec}
	fail := func(err error) (*session, error) {
		s.close()
		return nil, err
	}
	if err := s.harden(o); err != nil {
		return fail(err)
	}
	tab, err := browser.NewTab(bctx)
	if err != nil {
		return fail(fmt.Errorf("consent: opening a tab: %w", err))
	}
	s.tab = tab
	wait := o.Timeout
	if wait <= 0 {
		wait = 30 * time.Second
	}
	if err := rec.waitReady(ctx, wait); err != nil {
		return fail(fmt.Errorf("consent: setting up the tab: %w", err))
	}
	rec.mu.Lock()
	s.main = rec.main.s
	rec.mu.Unlock()
	return s, nil
}

// harden sets the context-wide rules: downloads denied (and reported),
// every permission denied, the plan's cookies for the target only.
func (s *session) harden(o sessionOptions) error {
	root := s.h.root
	id := s.ctx.BrowserContextID
	if err := (proto.BrowserSetDownloadBehavior{Behavior: proto.BrowserSetDownloadBehaviorBehaviorDeny, BrowserContextID: id, EventsEnabled: true}).Call(root); err != nil {
		return fmt.Errorf("consent: denying downloads: %w", err)
	}
	if err := (proto.BrowserResetPermissions{BrowserContextID: id}).Call(root); err != nil {
		return fmt.Errorf("consent: resetting permissions: %w", err)
	}
	for _, name := range deniedPermissions {
		// Names a Chrome doesn't know are skipped; the rest are denied.
		_ = proto.BrowserSetPermission{Permission: &proto.BrowserPermissionDescriptor{Name: name}, Setting: proto.BrowserPermissionSettingDenied, BrowserContextID: id}.Call(root)
	}
	if cookies := cookieParams(o.Plan.Cookies, o.Target.URL); len(cookies) > 0 {
		if err := (proto.StorageSetCookies{Cookies: cookies, BrowserContextID: id}).Call(root); err != nil {
			return fmt.Errorf("consent: setting --cookie: %w", err)
		}
		s.rec.noteEchoCookies(cookies)
	}
	return nil
}

// cookieParams turns --cookie name=value pairs into host-only cookies
// for the target, so no third party ever gets them.
func cookieParams(cookies []string, target *url.URL) []*proto.NetworkCookieParam {
	if target == nil {
		return nil
	}
	var out []*proto.NetworkCookieParam
	for _, c := range cookies {
		pair, _, _ := strings.Cut(c, ";")
		name, value, ok := strings.Cut(strings.TrimSpace(pair), "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			continue
		}
		out = append(out, &proto.NetworkCookieParam{Name: name, Value: strings.TrimSpace(value),
			URL: target.Scheme + "://" + target.Host + "/", Secure: target.Scheme == "https"})
	}
	return out
}

// navigate loads raw in the tab. The error is Chrome's own (a refused
// connection, a TLS failure) or the context's.
func (s *session) navigate(raw string) error {
	s.rec.navigating()
	res, err := proto.PageNavigate{URL: raw}.Call(s.main)
	if err != nil {
		return err
	}
	if res.ErrorText != "" {
		return errors.New(res.ErrorText)
	}
	return nil
}

// evalMain runs the expression js in the tab's main world and returns
// its JSON value, at most maxEvalResult: the page measures it before
// sending it and Echo checks again.
func (s *session) evalMain(ctx context.Context, js string) (json.RawMessage, error) {
	c := cdp{b: s.main.b, id: s.main.id, ctx: ctx}
	res, err := proto.RuntimeEvaluate{Expression: mainJS(js), ReturnByValue: true, AwaitPromise: true}.Call(c)
	if err != nil {
		return nil, err
	}
	if res.ExceptionDetails != nil {
		msg := exceptionText(res.ExceptionDetails)
		if strings.Contains(msg, "result too large") {
			return nil, errResultTooLarge
		}
		return nil, fmt.Errorf("script failed: %s", consent.SafeText(msg, 200))
	}
	if res.Result == nil {
		return json.RawMessage("null"), nil
	}
	out, err := json.Marshal(res.Result.Value)
	if err != nil {
		return nil, err
	}
	if len(out) > maxEvalResult {
		return nil, errResultTooLarge
	}
	return out, nil
}

// close ends recording, disposes of the context (its tab, cookies and
// cache go with it) and returns what was recorded.
func (s *session) close() *Recording {
	out := s.rec.finish()
	_ = s.ctx.Close()
	s.h.unregister(s.ctx.BrowserContextID)
	return out
}
