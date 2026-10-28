// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package browser runs headless Chrome for Echo's consent checks. It
// uses the Chrome or Chromium already installed when there is one.
// Otherwise it downloads a pinned Chrome for Testing build from Google
// into Rhythm's cache on first use, checking its SHA-256 before
// unpacking it.
//
// Copied from github.com/RhythmShahriar/soundcheck/browser at v1.2.2; keep cft_pins.go identical (TestPinsMatchSoundcheck).
package browser

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/launcher/flags"
	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/rhythm/kit/config"

	"github.com/RhythmShahriar/echo/internal/browser/retry"
)

// Options choose and configure the browser. The zero value finds an
// installed Chrome or downloads one.
type Options struct {
	// Bin is a Chrome/Chromium binary to use; empty finds one or downloads.
	Bin string
	// NoDownload fails instead of downloading Chrome for Testing (offline, CI).
	NoDownload bool
	// NoSandbox runs Chrome without its sandbox, which it needs as root.
	// RHYTHM_ECHO_NO_SANDBOX=1 or SOUNDCHECK_NO_SANDBOX=1 does the same.
	NoSandbox bool
	// Proxy routes the browser's traffic, e.g. http://127.0.0.1:8080.
	Proxy string
	// Insecure accepts invalid TLS certificates.
	Insecure bool
	// Visible shows the browser window, for debugging flows.
	Visible bool
	// Log receives one-line progress messages, e.g. about a download,
	// the browser version and sandbox warnings.
	Log func(string)
	// Flags are extra Chrome switches, such as "--lang=de-DE" or
	// "--proxy-server=http://127.0.0.1:1234", added after Echo's own. A
	// switch on deniedFlags is an error: each one would let a page reach
	// past the browser's own walls.
	Flags []string
}

// deniedFlags are the Chrome switches Flags may never carry: they turn
// off the same-origin policy, open local files to pages, open DevTools
// to other origins or a fixed port, or merge sites into one process.
var deniedFlags = []string{
	"--disable-web-security",
	"--allow-file-access-from-files",
	"--remote-allow-origins",
	"--disable-site-isolation-trials",
	"--remote-debugging-port",
}

// splitFlag turns "--name=value" into name and value, without the dashes.
// It refuses a switch on deniedFlags and anything that isn't a switch.
func splitFlag(f string) (name, value string, err error) {
	if !strings.HasPrefix(f, "--") || len(f) < 3 {
		return "", "", fmt.Errorf("browser flag %q: want --name or --name=value", f)
	}
	name, value, _ = strings.Cut(f, "=")
	if slices.Contains(deniedFlags, strings.ToLower(name)) {
		return "", "", fmt.Errorf("browser flag %s isn't allowed: it would let a page past the browser's own protections", name)
	}
	return strings.TrimPrefix(name, "--"), value, nil
}

// ErrNoBrowser marks the errors Find and Launch return when there is no
// browser to start: none installed with downloads off, a --browser path
// that doesn't exist, or a Chrome for Testing download that failed.
var ErrNoBrowser = errors.New("no browser")

// ErrSandboxRoot marks the error Launch returns when it runs as root
// without the opt-in to turn Chrome's sandbox off.
var ErrSandboxRoot = errors.New("root without --no-sandbox")

// markedError is an error that also matches a sentinel with errors.Is,
// keeping its own message.
type markedError struct {
	err, mark error
}

func (e *markedError) Error() string   { return e.err.Error() }
func (e *markedError) Unwrap() []error { return []error{e.err, e.mark} }

// mark makes err match mark with errors.Is; nil stays nil.
func mark(err, m error) error {
	if err == nil {
		return nil
	}
	return &markedError{err, m}
}

// StartError is Launch's error when the browser at Bin was found but
// didn't start: it couldn't be run, or it exited before it opened
// DevTools.
type StartError struct {
	Bin  string
	Err  error
	Hint string // what to try, "" when there is nothing specific
}

func (e *StartError) Error() string {
	return "starting " + e.Bin + ": " + StartReason(e.Err) + e.Hint
}

func (e *StartError) Unwrap() error { return e.Err }

// StartReason says why a browser didn't start, without the empty
// "Failed to get the debug url: " rod writes when Chrome printed
// nothing before it exited.
func StartReason(err error) string {
	if err == nil {
		return "it didn't start"
	}
	msg := strings.TrimSpace(err.Error())
	if rest, ok := strings.CutPrefix(msg, "[launcher] Failed to get the debug url:"); ok {
		rest = strings.TrimSpace(rest)
		if rest == "" {
			return "it exited before it opened DevTools"
		}
		return "it exited before it opened DevTools: " + rest
	}
	return strings.TrimSpace(strings.TrimRight(msg, ": "))
}

// Browser is a running Chrome.
type Browser struct {
	*rod.Browser
	proc *process
	Bin  string
	// ControlURL is the DevTools WebSocket address Chrome printed. rod
	// asks for an ephemeral port on 127.0.0.1, never a fixed one.
	ControlURL string
	// Version is the running browser's version, e.g. 153.0.8010.52. It
	// hides rod's Browser.Version method, which asks Chrome each time.
	Version string
	// Downloaded is set when Bin is Soundcheck's own Chrome for Testing.
	Downloaded bool
	// NoSandbox is set when Chrome runs without its sandbox (--no-sandbox
	// or the environment), so a report can say so.
	NoSandbox bool
}

// Name names the browser without its path, e.g. "Chrome 153.0.8010.52"
// or "Chrome for Testing 153.0.8010.52": what results and reports keep,
// as the path can name the user and their folders. Describe adds it.
func (b *Browser) Name() string {
	if b.Downloaded {
		return "Chrome for Testing " + b.Version
	}
	return "Chrome " + b.Version
}

// Describe names the browser for the run's log and doctor, with its
// path, e.g. "Chrome 153.0.8010.52 (/usr/bin/google-chrome)" or "Chrome
// for Testing 153.0.8010.52 (downloaded)".
func (b *Browser) Describe() string {
	if b.Downloaded {
		return "Chrome for Testing " + b.Version + " (downloaded)"
	}
	return "Chrome " + b.Version + " (" + b.Bin + ")"
}

// DownloadDir is where the pinned Chrome for Testing is kept. It is
// Soundcheck's folder on purpose: both tools pin the same build, so one
// download serves both.
func DownloadDir() (string, error) {
	d, err := config.CacheDir("soundcheck")
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "browser"), nil
}

// Find returns the browser binary to use: opts.Bin, an installed Chrome,
// or the pinned Chrome for Testing (downloading it unless NoDownload is
// set). It is FindContext with a context that is never canceled.
func Find(opts Options) (bin string, downloaded bool, err error) {
	return FindContext(context.Background(), opts)
}

// FindContext is Find with a context: canceling ctx stops a Chrome for
// Testing download.
func FindContext(ctx context.Context, opts Options) (bin string, downloaded bool, err error) {
	if opts.Bin != "" {
		if _, err := os.Stat(opts.Bin); err != nil {
			return "", false, mark(fmt.Errorf("browser %s: %w", opts.Bin, err), ErrNoBrowser)
		}
		return opts.Bin, false, nil
	}
	if p, ok := lookPath(); ok {
		return p, false, nil
	}
	dir, err := DownloadDir()
	if err != nil {
		return "", false, err
	}
	platform := Platform()
	if pinned.Version != "" {
		if p := binPath(installDir(dir, pinned.Version), platform); fileExists(p) {
			return p, true, nil
		}
	}
	if opts.NoDownload {
		return "", false, mark(errors.New("no Chrome or Chromium found; install Chrome, pass --browser /path/to/chrome, or allow the one-time Chrome for Testing download"), ErrNoBrowser)
	}
	p, err := install(ctx, pinned, platform, dir, opts.Log)
	if err != nil {
		return "", false, mark(err, ErrNoBrowser)
	}
	return p, true, nil
}

// lookPath finds an installed Chrome; tests replace it to force a
// download.
var lookPath = launcher.LookPath

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// Launch starts headless Chrome. A start or connection that fails is
// tried again, twice at most, in a new profile after a pause: on a busy
// machine Chrome can be too slow to print its DevTools address or to
// answer the first connection.
func Launch(ctx context.Context, opts Options) (*Browser, error) {
	noSandbox, err := sandbox(opts)
	if err != nil {
		return nil, err
	}
	for _, f := range opts.Flags {
		if _, _, err := splitFlag(f); err != nil {
			return nil, err
		}
	}
	bin, downloaded, err := FindContext(ctx, opts)
	if err != nil {
		return nil, err
	}
	var b *rod.Browser
	var proc *process
	var control string
	err = retry.Do(ctx, retry.Policy{Tries: launchTries, Pause: launchPause, Again: launchAgain,
		Note: func(_ int, err error) { logf(opts, retryNote(bin, err)) },
	}, func() error {
		var err error
		b, proc, control, err = launchOnce(ctx, opts, bin, noSandbox)
		return err
	})
	if err != nil {
		return nil, err
	}
	br := &Browser{Browser: b, proc: proc, Bin: bin, ControlURL: control, Downloaded: downloaded, NoSandbox: noSandbox}
	if v, err := (proto.BrowserGetVersion{}).Call(b); err == nil {
		br.Version = productVersion(v.Product)
	}
	if opts.Log != nil {
		opts.Log("Browser: " + br.Describe())
	}
	return br, nil
}

// launchTries and launchPause bound Launch's tries: the pause doubles
// after each one.
var (
	launchTries = 3
	launchPause = 500 * time.Millisecond
)

// launchAgain reports whether a failed start may work on another try.
// A sandbox Chrome can't set up fails the same way every time.
func launchAgain(err error) bool {
	return !strings.Contains(strings.ToLower(err.Error()), "sandbox")
}

// Args is the command line Launch would start Chrome with for opts
// (without the binary; the profile folder is a fresh random path each
// time), so callers can check the exact switches. The sandbox switch
// follows opts.NoSandbox alone here.
func Args(opts Options) ([]string, error) {
	l, err := newLauncher(context.Background(), opts, opts.NoSandbox)
	if err != nil {
		return nil, err
	}
	return l.FormatArgs(), nil
}

// launchOnce is one try of Launch: it starts Chrome in a new profile and
// connects to it.
func launchOnce(ctx context.Context, opts Options, bin string, noSandbox bool) (*rod.Browser, *process, string, error) {
	l, err := newLauncher(ctx, opts, noSandbox)
	if err != nil {
		return nil, nil, "", err
	}
	u, proc, err := start(ctx, l, bin)
	if err != nil {
		return nil, nil, "", &StartError{Bin: bin, Err: err, Hint: sandboxHint(err, noSandbox)}
	}
	b := rod.New().ControlURL(u).Context(ctx)
	if err := b.Connect(); err != nil {
		proc.stop()
		return nil, nil, "", fmt.Errorf("connecting to the browser: %w", err)
	}
	return b, proc, u, nil
}

// newLauncher sets up Chrome's switches for opts.
func newLauncher(ctx context.Context, opts Options, noSandbox bool) (*launcher.Launcher, error) {
	l := launcher.New().Context(ctx).Headless(!opts.Visible).
		Set("disable-gpu").Set("hide-scrollbars").Set("mute-audio").
		Set("disable-features", "Translate,MediaRouter,OptimizationHints").
		Set("disable-background-networking").Set("no-first-run").Set("no-default-browser-check").
		// Pages are checked in parallel tabs. All but one are in the
		// background: without these Chrome throttles their timers and
		// rendering so the tabs end up taking turns.
		Set("disable-background-timer-throttling").Set("disable-backgrounding-occluded-windows").
		Set("disable-renderer-backgrounding").Set("disable-ipc-flooding-protection").
		// rod turns site isolation trials off by default; a page checked
		// for trackers must keep cross-site frames in their own processes,
		// as a visitor's Chrome does.
		Delete("disable-site-isolation-trials")
	// rod turns the sandbox off by itself inside containers; only the
	// rule in sandbox decides here.
	l = l.NoSandbox(noSandbox)
	if opts.Proxy != "" {
		l = l.Proxy(opts.Proxy)
	}
	if opts.Insecure {
		l = l.Set("ignore-certificate-errors")
	}
	for _, f := range opts.Flags {
		name, value, err := splitFlag(f)
		if err != nil {
			return nil, err
		}
		if value == "" {
			l = l.Set(flags.Flag(name))
		} else {
			l = l.Set(flags.Flag(name), value)
		}
	}
	return l, nil
}

// productVersion turns "HeadlessChrome/153.0.8010.52" into the number.
func productVersion(product string) string {
	if _, v, ok := strings.Cut(product, "/"); ok {
		return v
	}
	return product
}

// geteuid is os.Geteuid; tests replace it to act as root.
var geteuid = os.Geteuid

// NoSandboxEnv turns Chrome's sandbox off when set to 1.
const NoSandboxEnv = "RHYTHM_ECHO_NO_SANDBOX"

// SoundcheckNoSandboxEnv is Soundcheck's name for NoSandboxEnv, honored
// too so one setting covers both tools on a CI runner.
const SoundcheckNoSandboxEnv = "SOUNDCHECK_NO_SANDBOX"

// sandbox reports whether Chrome must run without its sandbox. Chrome
// can't use it as root, which is common in containers and CI images.
// A page that exploits a renderer bug then runs with no barrier around
// it. So root needs an explicit opt-in. Every run without
// the sandbox says so loudly.
func sandbox(opts Options) (off bool, err error) {
	off = opts.NoSandbox || os.Getenv(NoSandboxEnv) == "1" || os.Getenv(SoundcheckNoSandboxEnv) == "1"
	if geteuid() == 0 && !off {
		return false, mark(errors.New("running as root, where Chrome can't use its sandbox. Run Echo as a normal user, or pass --no-sandbox (or set "+NoSandboxEnv+"=1) to run Chrome without it (only for sites you trust, e.g. in a throwaway container)"), ErrSandboxRoot)
	}
	if off {
		logf(opts, "WARNING: Chrome is running without its sandbox. A page that exploits a browser bug could take over this machine; only check sites you trust.")
	}
	return off, nil
}

// sandboxHint points at NoSandboxEnv when Chrome failed to start
// because it couldn't set up its sandbox. Inside containers that is
// common for normal users too (Docker's default seccomp profile,
// Ubuntu's AppArmor limits on user namespaces). Chrome's own error
// ("No usable sandbox!") doesn't say how to get past it.
func sandboxHint(err error, noSandbox bool) string {
	if noSandbox || !strings.Contains(strings.ToLower(err.Error()), "sandbox") {
		return ""
	}
	return "\n(pass --no-sandbox, or set " + NoSandboxEnv + "=1, if this container can't give Chrome its sandbox; only for sites you trust)"
}

// retryNote is the line Launch writes before it starts Chrome again:
// "Chrome at /usr/bin/chrome didn't start (it exited before it opened
// DevTools); starting it again".
func retryNote(bin string, err error) string {
	reason := StartReason(err)
	var se *StartError
	if errors.As(err, &se) {
		reason = StartReason(se.Err)
	}
	return "Chrome at " + bin + " didn't start (" + reason + "); starting it again"
}

// logf sends a line to opts.Log, or to stderr when nothing listens, so
// warnings are never lost.
func logf(opts Options, msg string) {
	if opts.Log != nil {
		opts.Log(msg)
		return
	}
	fmt.Fprintln(os.Stderr, msg)
}

// Session is an isolated browsing context with its own cookies, like a
// private window: anonymous crawling and a logged-in user never share one.
// A Chrome that has only just started may not create one at the first
// ask on a busy machine, so it is asked twice more after a pause.
func (b *Browser) Session() (*rod.Browser, error) {
	var sess *rod.Browser
	err := retry.Do(b.GetContext(), retry.Policy{Tries: launchTries, Pause: launchPause}, func() error {
		var err error
		sess, err = b.Incognito()
		return err
	})
	return sess, err
}

// SessionWithProxy is Session for a context whose traffic goes through
// proxy (e.g. "http://127.0.0.1:41234"), bypassing it for the hosts in
// bypass, which is Chrome's --proxy-bypass-list syntax ("<-loopback>"
// sends loopback through the proxy too). Its tabs get no default device
// emulation: the caller sets the user agent and viewport itself. Close
// the returned browser to dispose of the context.
func (b *Browser) SessionWithProxy(proxy, bypass string) (*rod.Browser, error) {
	var sess *rod.Browser
	err := retry.Do(b.GetContext(), retry.Policy{Tries: launchTries, Pause: launchPause}, func() error {
		res, err := proto.TargetCreateBrowserContext{ProxyServer: proxy, ProxyBypassList: bypass}.Call(b.Browser)
		if err != nil {
			return err
		}
		c := *b.Browser
		c.BrowserContextID = res.BrowserContextID
		// rod emulates a laptop in every new tab by default, which
		// would replace the user agent and viewport the caller sets.
		sess = c.NoDefaultDevice()
		return nil
	})
	return sess, err
}

// PID is the browser process's id, for tests that check it is gone
// after Close.
func (b *Browser) PID() int { return b.proc.getPID() }

// ProfileDir is Chrome's temporary profile folder, which Close removes.
func (b *Browser) ProfileDir() string { return b.proc.profile() }

// Close shuts the browser down, kills any of its processes still
// running and removes its temporary profile.
func (b *Browser) Close() {
	// The launch context may be canceled already (Ctrl-C): ask Chrome to
	// close on a context of its own, for a moment, then make sure.
	ctx, cancel := context.WithTimeout(context.Background(), closeWait)
	defer cancel()
	_ = b.Browser.Context(ctx).Close()
	b.proc.stop()
}

// closeWait is how long Close waits for Chrome to answer its close
// request before stop kills it.
const closeWait = 2 * time.Second

// Prune deletes downloaded browsers other than the Chrome for Testing
// version this build uses (all of them with all set, when an installed
// Chrome is used instead), including Chromium revisions older versions
// of Soundcheck downloaded. Hidden entries, such as the .unpack-* folder
// another process is unpacking a download into, are left alone. It
// returns what it removed.
func Prune(all bool) ([]string, error) {
	dir, err := DownloadDir()
	if err != nil {
		return nil, err
	}
	keep := ""
	if pinned.Version != "" {
		keep = filepath.Base(installDir(dir, pinned.Version))
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || (e.Name() == keep && !all) {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if err := os.RemoveAll(p); err != nil {
			return removed, err
		}
		removed = append(removed, p)
	}
	return removed, nil
}

// NewTab opens a blank page in sess, in its own window, that renders as if
// it were in front. Headless Chrome treats every tab but the front one as
// hidden: they don't paint (no FCP, LCP or long tasks), requestAnimation-
// Frame never fires, so animations stand still and input stalls. Its own
// window plus focus emulation lifts that, so pages checked in parallel
// behave like the one a visitor sees.
//
// Chrome on a macOS runner has been seen to never answer the first
// command sent to a new window, which hung the run. A try that takes
// longer than newTabWait is given up and a second window is opened; the
// stuck one is closed if it ever answers.
func NewTab(sess *rod.Browser) (*rod.Page, error) {
	var err error
	for range 2 {
		var tab *rod.Page
		tab, err = newTabOnce(sess, newTabWait)
		if !errors.Is(err, errTabStuck) {
			return tab, err
		}
	}
	return nil, err
}

// newTabWait is how long one try at opening a tab may take.
var newTabWait = 30 * time.Second

var errTabStuck = errors.New("no new tab from Chrome")

// newTabOnce runs openTab, giving up after wait.
func newTabOnce(sess *rod.Browser, wait time.Duration) (*rod.Page, error) {
	type result struct {
		tab *rod.Page
		err error
	}
	done := make(chan result, 1)
	open := openTab
	go func() {
		tab, err := open(sess)
		done <- result{tab, err}
	}()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case r := <-done:
		return r.tab, r.err
	case <-timer.C:
		go func() {
			if r := <-done; r.tab != nil {
				_ = r.tab.Close()
			}
		}()
		return nil, fmt.Errorf("%w in %s", errTabStuck, wait)
	}
}

// openTab opens the window and turns on focus emulation. A variable so
// the tests can stand in for a Chrome that doesn't answer.
var openTab = func(sess *rod.Browser) (*rod.Page, error) {
	tab, err := sess.Page(proto.TargetCreateTarget{URL: "about:blank", NewWindow: true})
	if err != nil {
		return nil, err
	}
	if err := (proto.EmulationSetFocusEmulationEnabled{Enabled: true}).Call(tab); err != nil {
		_ = tab.Close()
		return nil, err
	}
	return tab, nil
}
