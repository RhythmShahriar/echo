// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"

	"github.com/charmbracelet/x/term"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/evidence"
	"github.com/RhythmShahriar/echo/report"
	"github.com/RhythmShahriar/echo/store"
	"github.com/RhythmShahriar/rhythm/kit/cache"
	kitcli "github.com/RhythmShahriar/rhythm/kit/cli"
	kitconfig "github.com/RhythmShahriar/rhythm/kit/config"
)

// writers are the report writers the check uses: the report package's,
// which tests swap for stand-ins (the verdict lane writes the real ones
// and their goldens).
var writers = struct {
	text    func(io.Writer, *consent.Result, report.ConsentTextOptions) error
	md      func(io.Writer, *consent.Result, report.ConsentMarkdownOptions) error
	html    func(io.Writer, *consent.Result, report.ConsentHTMLOptions) error
	junit   func(io.Writer, *consent.Result, string) error
	json    func(io.Writer, *consent.Result, string) error
	detect  func(io.Writer, *consent.DetectResult, bool, report.ConsentTextOptions) error
	verify  func(io.Writer, *report.ConsentVerify, bool, report.ConsentTextOptions) error
	catalog func(io.Writer, []consent.CMPRuleInfo, []consent.TrackerInfo, string, bool, report.ConsentTextOptions) error
}{report.WriteConsentText, report.WriteConsentMarkdown, report.WriteConsentHTML, report.WriteConsentJUnit, report.WriteConsentJSON,
	report.WriteConsentDetect, report.WriteConsentVerify, report.WriteConsentCatalog}

// The report files of an evidence folder.
const (
	reportHTML  = "report.html"
	reportMD    = "report.md"
	reportJUnit = "report.xml"
)

// noteInterrupted is the note an interrupted check's result carries.
const noteInterrupted = "Interrupted before every run finished: this result is incomplete."

// isCI reports whether this runs in CI, where no notice, tip or update
// prompt is shown.
func isCI() bool {
	v := strings.ToLower(os.Getenv("CI"))
	return v != "" && v != "false" && v != "0"
}

// writeError is a report that couldn't be written: exit 3 with the
// file named.
type writeError struct {
	path string
	err  error
}

func (e *writeError) Error() string {
	return fmt.Sprintf("couldn't write %s: %v", shown(e.path), e.err)
}

// check runs rhythm echo consent itself: it checks everything it can
// before a browser starts, runs the plans, writes the evidence folder
// and the reports in the order PRD 9.2 needs (attachments, consent.json,
// the reports stating its hash, the manifest last) and prints the
// result.
func (c *cmd) check(pos []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return c.runCheck(ctx, pos)
}

// runCheck is check with ctx: canceling it stops the runs, which are
// then judged and written as an interrupted check.
func (c *cmd) runCheck(ctx context.Context, pos []string) int {
	o, env := c.o, c.env
	st, err := c.prepare(pos)
	if err != nil {
		return fail(env, err)
	}
	for _, n := range st.notes {
		fmt.Fprintln(env.Stderr, "rhythm echo:", n)
	}
	var base *consent.Result
	if o.against != "" {
		if base, err = loadAgainst(st.store, o.against); err != nil {
			return fail(env, err)
		}
	}
	if o.save != "" {
		if err := store.CheckName(o.save); err != nil {
			return fail(env, usagef("--save: %v", err))
		}
	}
	stages := consent.RunStages
	if base != nil {
		stages = append(slices.Clone(stages), consent.StageComparer)
	}
	if err := consent.Need(stages...); err != nil {
		return fail(env, err)
	}

	jsonOut := stdoutFormat(st.outs) == fmtJSON
	machine := isCI() || o.quiet || jsonOut
	firstRun := false
	if !machine && kitconfig.FirstRun("echo-consent-notice") {
		fmt.Fprint(env.Stderr, notice)
		firstRun = true
	}
	for _, w := range st.warn {
		fmt.Fprintln(env.Stderr, "rhythm echo:", w)
	}
	if slices.ContainsFunc(st.plans, func(p *consent.Plan) bool { return p.Browser.NoSandbox }) || noSandboxEnv() {
		fmt.Fprintln(env.Stderr, "rhythm echo:", msgNoSandbox)
	}
	if slices.ContainsFunc(st.plans, func(p *consent.Plan) bool { return !p.Hold }) {
		fmt.Fprintln(env.Stderr, "rhythm echo:", msgSendHits)
	}

	var prompt func(context.Context)
	if env.Update != nil && !machine && kitcli.IsTerminal(env.Stdout) {
		prompt = env.Update(ctx)
	}

	started := env.now()
	// PRD 6: the store is made when the first run starts. Its folders
	// go again when no run could (the site didn't answer, a selector
	// the browser refuses): only the empty ones this check made.
	made := missingDirs(st.store.Root, filepath.Join(st.store.Root, store.Consent))
	dir, err := st.store.NewDir(store.Consent, store.Stamp(started))
	if err != nil {
		return fail(env, &writeError{filepath.Join(st.store.Root, store.Consent), err})
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	st.store.ExcludeFromGit()
	live := liveWriter(env.Stderr, o.live)
	var results []*consent.Result
	interrupted := false
	pages := 0
	for _, p := range st.plans {
		p.ID, p.Dir = filepath.Base(dir), dir
		// Every plan writes into the one folder: page numbers go on
		// from the earlier plans' so no file is written twice.
		p.PageBase, pages = pages, pages+len(p.Targets)
		p.Browser.Log = browserLog(env.Stderr, o.verbose)
		p.Live = live
		r, err := consent.RunPlan(ctx, p)
		if r != nil {
			results = append(results, r)
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			interrupted = true
			break
		}
		if err != nil {
			// A fatal error stops the whole check before consent.json
			// is written, so the folder (with what earlier --sites
			// sites left in it) would be a result without a result
			// file: verify and report would only trip over it.
			_ = os.RemoveAll(dir) //nolint:gosec // the folder this check made
			if len(results) == 0 {
				removeEmpty(made)
			}
			var fe *consent.FatalError
			if errors.As(err, &fe) {
				fmt.Fprintln(env.Stderr, "rhythm echo:", fatalMessage(fe))
				return fatalExit(fe)
			}
			return fail(env, err)
		}
	}
	r := merge(results, consent.Result{ID: filepath.Base(dir), Started: started.UTC(), Echo: env.Version, OS: runtime.GOOS + "/" + runtime.GOARCH})
	r.DurationMS = env.now().Sub(started).Milliseconds()
	if interrupted {
		// complete is false in consent.json; the note says why in
		// words a reader of the file sees.
		r.Complete = false
		r.Notes = append(r.Notes, noteInterrupted)
	}
	if base != nil {
		if r.Comparison = consent.Compare(base, r, o.accept); r.Comparison != nil {
			r.Comparison.Against = o.against
		}
	}
	r.Evidence.Folder = shown(dir)

	if err := c.writeEvidence(st, dir, r); err != nil {
		return fail(env, err)
	}
	view := env.Stdout
	if stdoutFormat(st.outs) != "" {
		view = env.Stderr
	}
	opts := c.textOptions(view)
	opts.FailOn, opts.FirstRun, opts.ReportPath = st.failOn, firstRun, shown(filepath.Join(dir, reportHTML))
	// "none found within" names the wait the check used (the first
	// plan's: a --sites site may set its own).
	opts.BannerWait = st.plans[0].BannerWait
	if err := writers.text(view, r, opts); err != nil {
		fmt.Fprintln(env.Stderr, "rhythm echo: printing the result:", err)
	}
	if o.save != "" {
		if !r.Complete {
			fmt.Fprintf(env.Stderr, "rhythm echo: --save %s: not saved: a baseline needs a complete check and some runs couldn't be checked\n", o.save)
		} else if line, err := saveBaseline(st.store, o.save, dir); err != nil {
			return fail(env, &writeError{filepath.Join(baselinesDir(st.store), o.save), err})
		} else if !o.quiet {
			fmt.Fprintln(view, line)
		}
	}
	if !isCI() && !o.quiet && !jsonOut {
		showTip(view, r, st, o)
	}
	if o.open && env.Open != nil {
		if err := env.Open(filepath.Join(dir, reportHTML)); err != nil {
			fmt.Fprintln(env.Stderr, "rhythm echo: opening the report:", err)
		}
	}
	if interrupted {
		fmt.Fprintf(env.Stderr, "rhythm echo: interrupted; the runs that finished are kept in %s\n", shown(dir))
		return exitInterrupted
	}
	if prompt != nil {
		prompt(ctx)
	}
	return r.ExitCode(st.failOn)
}

// textOptions are the terminal options for output written to view:
// plain off a terminal or with --plain, color unless --no-color or
// NO_COLOR, -q and -v.
func (c *cmd) textOptions(view io.Writer) report.ConsentTextOptions {
	o := c.o
	plain := o.plain || !kitcli.IsTerminal(view)
	return report.ConsentTextOptions{Plain: plain, Color: !plain && !o.noColor && os.Getenv("NO_COLOR") == "", Quiet: o.quiet, Verbose: o.verbose,
		Width: width(view)}
}

// writeEvidence writes consent.json (hashing the attachments first),
// the reports with its hash, the files and stdout -o and the format
// flags ask for, then the manifest.
func (c *cmd) writeEvidence(st *setup, dir string, r *consent.Result) error {
	sha, err := evidence.WriteResult(dir, r, c.o.signKey)
	if err != nil {
		var ue kitcli.UsageError
		if errors.As(err, &ue) {
			return err
		}
		return &writeError{filepath.Join(dir, evidence.ResultFile), err}
	}
	write := c.formatWriter(r, dir, sha, st.failOn)
	files := []output{{fmtHTML, filepath.Join(dir, reportHTML)}}
	if wants(st.outs, fmtMD) || os.Getenv("GITHUB_ACTIONS") == "true" {
		files = append(files, output{fmtMD, filepath.Join(dir, reportMD)})
	}
	if wants(st.outs, fmtJUnit) {
		files = append(files, output{fmtJUnit, filepath.Join(dir, reportJUnit)})
	}
	for _, f := range files {
		if err := writeFile(f.path, func(w io.Writer) error { return write(w, f.format) }); err != nil {
			return err
		}
	}
	if err := evidence.WriteManifest(dir); err != nil {
		return &writeError{filepath.Join(dir, evidence.ManifestFile), err}
	}
	for _, out := range st.outs {
		if out.path == "" {
			if err := write(c.env.Stdout, out.format); err != nil {
				return fmt.Errorf("writing the report: %w", err)
			}
			continue
		}
		if err := writeFile(out.path, func(w io.Writer) error { return write(w, out.format) }); err != nil {
			return err
		}
	}
	return nil
}

// formatWriter writes r in a format, stating consent.json's hash.
func (c *cmd) formatWriter(r *consent.Result, dir, sha, failOn string) func(io.Writer, string) error {
	return func(w io.Writer, format string) error {
		switch format {
		case fmtJSON:
			return writers.json(w, r, failOn)
		case fmtMD:
			return writers.md(w, r, report.ConsentMarkdownOptions{ConsentSHA256: sha, Details: c.o.verbose})
		case fmtJUnit:
			return writers.junit(w, r, sha)
		}
		return writers.html(w, r, report.ConsentHTMLOptions{EvidenceDir: dir, ConsentSHA256: sha})
	}
}

// writeFile writes a report atomically, 0600 in a 0700 folder: a
// report shows what a site's visitors would send.
func writeFile(path string, write func(io.Writer) error) error {
	var b bytes.Buffer
	if err := write(&b); err != nil {
		return &writeError{path, err}
	}
	if err := cache.WriteFile(path, b.Bytes()); err != nil {
		return &writeError{path, err}
	}
	return nil
}

// merge makes one result of the plans' results (one per --sites site):
// the first one's setup, every page, issue and inconclusive run, the
// network counts added up. With no result (interrupted before any
// finished) it is empty, which keeps the folder's consent.json honest:
// complete false.
func merge(rs []*consent.Result, empty consent.Result) *consent.Result {
	if len(rs) == 0 {
		r := empty
		r.Pages, r.Issues, r.Inconclusive, r.Notes = []consent.Page{}, []consent.Issue{}, []consent.Inconclusive{}, []string{}
		return &r
	}
	r := rs[0]
	for _, o := range rs[1:] {
		r.Pages = append(r.Pages, o.Pages...)
		r.Issues = append(r.Issues, o.Issues...)
		r.Inconclusive = append(r.Inconclusive, o.Inconclusive...)
		r.Network.Requests += o.Network.Requests
		r.Network.Held += o.Network.Held
		r.Network.Refused += o.Network.Refused
		r.Network.BrowserInternal += o.Network.BrowserInternal
		r.Mapped = r.Mapped || o.Mapped
		r.Sandbox = r.Sandbox && o.Sandbox
		r.TLSVerified = r.TLSVerified && o.TLSVerified
		r.Held = r.Held && o.Held
		r.Complete = r.Complete && o.Complete
		for _, n := range o.Notes {
			if !slices.Contains(r.Notes, n) {
				r.Notes = append(r.Notes, n)
			}
		}
	}
	return r
}

// noSandboxEnv reports whether the environment turns Chrome's sandbox
// off, as --no-sandbox does.
func noSandboxEnv() bool {
	return os.Getenv("RHYTHM_ECHO_NO_SANDBOX") == "1" || os.Getenv("SOUNDCHECK_NO_SANDBOX") == "1"
}

// width is the terminal's width when w is one, else 0 (80).
func width(w io.Writer) int {
	f, ok := w.(*os.File)
	if !ok {
		return 0
	}
	cols, _, err := term.GetSize(f.Fd())
	if err != nil {
		return 0
	}
	return cols
}

// liveWriter is --live: one line on stderr per tracker hit as the
// browser records it, or nil. Runs record in parallel, so lines are
// written whole under a lock.
func liveWriter(w io.Writer, on bool) func(consent.Event) {
	if !on {
		return nil
	}
	var mu sync.Mutex
	return func(ev consent.Event) {
		line := liveLine(ev)
		if line == "" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintln(w, line)
	}
}

// liveLine is one --live line, 'hit  <s.ss>s  <run>  <tracker>
// <host><path> (held)', or "" for an event that isn't a hit. The run is
// named with the part of it the hit fell in when that isn't the run's
// own choice: "Accept, before the choice". An event without a run (an
// older runner) shows its phase alone.
func liveLine(ev consent.Event) string {
	if ev.Kind != consent.EvRequest || ev.Hit == nil || ev.Request == nil || ev.Hit.Kind == consent.HitScript {
		return ""
	}
	where := ev.Request.Host
	if u, err := url.Parse(ev.Request.URL); err == nil && u.Host != "" {
		where = u.Host + u.EscapedPath()
	}
	held := ""
	if ev.Request.Held {
		held = " (held)"
	}
	return fmt.Sprintf("hit  %.2fs  %s  %s  %s%s", float64(ev.AtMS)/1000, runLabel(ev),
		consent.SafeText(ev.Hit.Name, 60), consent.SafeText(where, 200), held)
}

// runLabel names the run and phase of a --live line.
func runLabel(ev consent.Event) string {
	switch {
	case ev.Run == "":
		return phaseLabel(ev.Phase)
	case ev.Phase == consent.PhaseBefore && ev.Run != consent.RunNone:
		return ev.Run.Label() + ", before the choice"
	case ev.Run == consent.RunWithdraw && ev.Phase == consent.PhaseAfterAccept:
		return ev.Run.Label() + ", after Accept"
	}
	return ev.Run.Label()
}

// phaseLabel names a phase as the run lines do.
func phaseLabel(p consent.Phase) string {
	switch p {
	case consent.PhaseBefore:
		return "before the choice"
	case consent.PhaseAfterAccept:
		return consent.RunAccept.Label()
	case consent.PhaseAfterReject:
		return consent.RunReject.Label()
	case consent.PhaseGPC:
		return consent.RunGPC.Label()
	case consent.PhaseAfterWithdraw:
		return consent.RunWithdraw.Label()
	}
	return consent.SafeText(string(p), 20)
}

// showTip prints the first tip that fits r and hasn't been shown on
// this machine.
func showTip(w io.Writer, r *consent.Result, st *setup, o *options) {
	fits := map[string]bool{
		"echo-consent-tip-reject": anyRun(r, func(run consent.Run) bool { return run.Reason == consent.ReasonNoButton }),
		"echo-consent-tip-geo": anyRun(r, func(run consent.Run) bool { return run.Reason == consent.ReasonNoBanner }) ||
			slices.ContainsFunc(r.Issues, func(is consent.Issue) bool { return is.Check == consent.CheckNoBanner }),
		"echo-consent-tip-gpc": !slices.ContainsFunc(st.plans, func(p *consent.Plan) bool { return slices.Contains(p.Runs, consent.RunGPC) }) &&
			anyRun(r, func(run consent.Run) bool {
				return slices.ContainsFunc(run.Hits, func(h consent.HitRef) bool { return h.Category == consent.CatAdvertising })
			}),
		"echo-consent-tip-save": o.save == "" && o.against == "" && r.Complete && r.ExitCode(st.failOn) == exitOK,
	}
	for _, t := range tips {
		if fits[t.key] && kitconfig.FirstRun(t.key) {
			fmt.Fprintln(w, t.text)
			return
		}
	}
}

// anyRun reports whether any run of r is f.
func anyRun(r *consent.Result, f func(consent.Run) bool) bool {
	for _, p := range r.Pages {
		if slices.ContainsFunc(p.Runs, f) {
			return true
		}
	}
	return false
}

// browserLog is where browse writes its Chrome lines: the download lines
// always, its "Browser: ..." line only with -v. Without -v the view names
// the browser on the first run, so the line isn't printed twice.
func browserLog(w io.Writer, verbose bool) io.Writer {
	if verbose || w == nil {
		return w
	}
	return quietBrowser{w}
}

// quietBrowser drops the "Browser: ..." line browse writes once Chrome
// starts. Browse writes each line in one call.
type quietBrowser struct{ w io.Writer }

func (q quietBrowser) Write(b []byte) (int, error) {
	if bytes.HasPrefix(b, []byte("Browser: ")) {
		return len(b), nil
	}
	return q.w.Write(b)
}

// missingDirs lists the folders of dirs that don't exist yet, in order.
func missingDirs(dirs ...string) []string {
	var out []string
	for _, d := range dirs {
		if _, err := os.Stat(d); errors.Is(err, fs.ErrNotExist) {
			out = append(out, d)
		}
	}
	return out
}

// removeEmpty removes dirs, the last first, where they are empty: a
// folder something else wrote into in the meantime stays.
func removeEmpty(dirs []string) {
	for i := len(dirs) - 1; i >= 0; i-- {
		_ = os.Remove(dirs[i])
	}
}
