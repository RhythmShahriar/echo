// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"cmp"
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/RhythmShahriar/echo/check"
	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/compare"
	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/dns"
	"github.com/RhythmShahriar/echo/headers"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/replay"
	"github.com/RhythmShahriar/echo/report"
	"github.com/RhythmShahriar/echo/runner"
	"github.com/RhythmShahriar/echo/store"
	"github.com/RhythmShahriar/rhythm/kit/cli"
	kitconfig "github.com/RhythmShahriar/rhythm/kit/config"
	"github.com/RhythmShahriar/rhythm/kit/update"
)

const keepRuns = 20

// command parses c's flags and runs fn, turning errors into exit codes.
func command(c cmdSpec, fn func(*session) int, args []string, stdout, stderr io.Writer) int {
	p, err := parse(c, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			commandHelp(stdout, c)
			return exitOK
		}
		fmt.Fprintln(stderr, "rhythm echo:", err)
		return exitUsage
	}
	warnTokenFlag(stderr, p.o.token, p.o.quiet)
	for _, n := range p.notes {
		fmt.Fprintln(stderr, "rhythm echo:", n)
	}
	s := p.begin(stdout, stderr)
	defer s.stop()
	return fn(s)
}

type session struct {
	*plan
	stdout, stderr io.Writer
	ctx            context.Context
	stop           func()
	tty, live      bool
	plain          bool // no color, boxes or glyphs: --plain, or not a terminal
	view           *report.Pretty
	notice         <-chan string
	ran            bool   // run, snapshot or compare ran the actions: failed ones set the exit code
	allFailed      string // what to say when every action failed
}

func (p *plan) begin(stdout, stderr io.Writer) *session {
	s := &session{plan: p, stdout: stdout, stderr: stderr}
	s.ctx, s.stop = signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	s.tty = cli.IsTerminal(stdout)
	s.plain = p.o.plain || !s.tty
	if s.plain || p.o.noColor {
		lipgloss.SetColorProfile(termenv.Ascii)
	}
	s.live = p.format == "" || p.o.output != ""
	out := stdout
	if !s.live {
		out = io.Discard
	}
	s.view = report.NewPretty(out, stderr, report.PrettyOptions{Plain: s.plain, Quiet: p.o.quiet, Verbose: p.o.verbose})
	return s
}

func (s *session) fail(err error) int {
	if errors.Is(err, context.Canceled) {
		fmt.Fprintln(s.stderr, "rhythm echo: canceled")
		return exitCanceled
	}
	fmt.Fprintln(s.stderr, "rhythm echo:", err)
	if isUsage(err) {
		return exitUsage
	}
	return exitError
}

// isUsage reports whether err is the user's to fix in the command line or
// the config, which exits 2 rather than 3.
func isUsage(err error) bool {
	var ue cli.UsageError
	return errors.As(err, &ue) || errors.Is(err, runner.ErrNoActions) || errors.Is(err, runner.ErrUnknownAction) || errors.Is(err, runner.ErrWaits) ||
		errors.Is(err, replay.ErrNoSecret) || errors.Is(err, replay.ErrRefused) || errors.Is(err, replay.ErrPayPal)
}

// selectActions checks the actions to run before anything is printed.
func (s *session) selectActions() error {
	_, err := runner.Select(s.cfg, s.o.actions)
	return err
}

// start prints the header and the one-time notice and checks for a new
// release in the background.
func (s *session) start(target string) {
	if kitconfig.FirstRun("echo-authorised-use") && !s.o.quiet {
		fmt.Fprint(s.stderr, "Note: use Echo on sites you own or are authorised to test. Captured messages can\n"+
			"hold personal data: they stay on this machine, with credentials masked. This notice is shown once.\n\n")
	}
	s.notice = update.Notice(s.ctx, &update.Updater{Repo: "RhythmShahriar/echo", Binary: "rhythm-echo", Current: version,
		UserAgent: "rhythm-echo/" + version + " (+https://rythm.sh/echo)", API: os.Getenv("RHYTHM_GITHUB_API")})
	s.view.Header(version, target)
}

// actions runs the configured actions against the running Echo.
func (s *session) actions(name string) (*model.Run, error) {
	h, err := s.echo.Health(s.ctx)
	if err != nil {
		return nil, err
	}
	s.view.Step("Echo", fmt.Sprintf("%s mode at %s", h.Mode, s.echo.URL))
	s.ran = true
	// Flush and command actions run in the project's folder, as they do
	// from the folder itself: "php artisan ..." fails from proj/sub.
	// runner.Options has no Dir yet, so the whole process moves there for
	// the run; every path the command reads after it is absolute or
	// relative to the working folder it is put back in.
	back, err := chdir(s.projectDir())
	if err != nil {
		return nil, err
	}
	defer back()
	return runner.Run(s.ctx, s.runnerOptions(name, h))
}

// runnerOptions are what the runner runs with: the config and the
// command line's --action, --parallel, --quiet and --timeout, with the
// run signed for the Echo h describes.
func (s *session) runnerOptions(name string, h client.Health) runner.Options {
	key := s.runKey(h)
	o := runner.Options{Config: s.cfg, Client: s.echo, Version: version, Name: name, Only: s.o.actions,
		Replay: replayer(s.cfg, s.projectDir(), s.cfgPath, key), Hooks: runner.Hooks{Action: s.view.Action, Log: s.view.Log},
		Parallel: s.o.parallel, RunKey: key}
	if s.o.quietForSet {
		o.Quiet = s.o.quietFor
	}
	if s.o.timeoutSet {
		o.Timeout = s.o.timeout
	}
	return o
}

// writeTokenEnv holds a site's write token (catcher C16), as the
// connectors read it. The runner signs the run with it when it is set,
// since that is the key the site's connector checks.
const writeTokenEnv = "RHYTHM_ECHO_WRITE_TOKEN" //nolint:gosec // a variable's name, not a token

// runKey is the key the run value is signed with (X-Echo-Run), so a
// connector that has a token trusts it: the site's write token when
// RHYTHM_ECHO_WRITE_TOKEN is set, else Echo's token. It is "" when there
// is no token, or the Echo h describes doesn't verify and strip signed
// values (an older one would keep the signature in the action ID).
func (s *session) runKey(h client.Health) string {
	if !h.Supports("signed-runs") {
		return ""
	}
	return cmp.Or(os.Getenv(writeTokenEnv), s.echo.Token)
}

// finish saves the HTML report, writes the chosen format, prints the
// summary and picks the exit code.
func (s *session) finish(res report.Result, hint string) int {
	html := ""
	if res.Dir != "" {
		html = filepath.Join(res.Dir, "report.html")
		if err := writeFile(html, func(w io.Writer) error { return report.HTML(w, res) }); err != nil {
			fmt.Fprintln(s.stderr, "rhythm echo: writing the HTML report:", err)
			html = ""
		}
	}
	if s.format != "" {
		write := func(w io.Writer) error {
			switch s.format {
			case "json":
				return writeEnvelope(w, resultKind(res), res)
			case "md":
				return report.Markdown(w, res)
			case "junit":
				return report.JUnit(w, res, s.junitFailOn(res))
			}
			return report.HTML(w, res)
		}
		var err error
		if s.o.output != "" {
			err = writeFile(s.o.output, write)
		} else {
			err = write(s.stdout)
		}
		if err != nil {
			fmt.Fprintln(s.stderr, "rhythm echo: writing the report:", err)
			return exitError
		}
	}
	open := html
	if s.format == "html" && s.o.output != "" {
		open = s.o.output
	}
	if hint == "" && open != "" {
		hint = "full report: " + rel(open)
	}
	if s.live {
		s.view.Summary(res, hint)
	}
	if s.o.output != "" && !s.o.quiet {
		fmt.Fprintf(s.stderr, "Report written to %s\n", s.o.output)
	}
	if s.o.open && open != "" {
		if err := openFile(open); err != nil {
			fmt.Fprintln(s.stderr, "rhythm echo: opening the report:", err)
		}
	}
	if s.notice != nil && s.live && !s.o.quiet {
		select {
		case msg, ok := <-s.notice:
			if ok {
				fmt.Fprintln(s.stderr, msg)
			}
		case <-time.After(200 * time.Millisecond):
		}
	}
	if s.ctx.Err() != nil {
		fmt.Fprintln(s.stderr, "rhythm echo: canceled; results are partial")
		return exitCanceled
	}
	if s.ran {
		// An action that failed means the run checked less than it should:
		// a site that is down must not pass CI. When none worked, nothing
		// was checked at all.
		if failed, total := failedActions(res.Run); total > 0 && failed == total {
			fmt.Fprintln(s.stderr, "rhythm echo:", cmp.Or(s.allFailed, "every action failed, so nothing was checked; is the site up?"))
			return exitError
		}
		if failed, _ := failedActions(res.Run); failed > 0 && s.o.failOn != failOnNone {
			return exitFindings
		}
	}
	if s.o.failOn != failOnNone && report.FailOn(res, s.o.failOn) {
		return exitFindings
	}
	return exitOK
}

// failOnNone is the --fail-on value that never fails for findings or
// failed actions, as in the GitHub Action. A run where every action
// failed still exits 3: nothing was checked.
const failOnNone = "none"

// failedActions counts the run's actions that failed.
func failedActions(r *model.Run) (failed, total int) {
	if r == nil {
		return 0, 0
	}
	for _, a := range r.Actions {
		if a.Error != "" {
			failed++
		}
	}
	return failed, len(r.Actions)
}

// junitFailOn is the threshold the JUnit report marks failures at. The
// report package doesn't know "none": for a comparison any unknown class
// fails nothing. For a run it would fail every issue, so a run gets the
// default instead.
func (s *session) junitFailOn(res report.Result) string {
	if s.o.failOn == failOnNone && !res.Compared() {
		return ""
	}
	return s.o.failOn
}

// checkFailOn reads --fail-on in any case and, when it isn't given,
// sets the default: broke for a comparison, error otherwise, as the
// GitHub Action does.
func (s *session) checkFailOn(compared bool) error {
	s.o.failOn = strings.ToLower(strings.TrimSpace(s.o.failOn))
	switch s.o.failOn {
	case "":
		s.o.failOn = defaultFailOn(compared)
		return nil
	case failOnNone:
		return nil
	}
	if report.ValidFailOn(s.o.failOn, compared) != nil {
		if compared {
			return cli.Usagef("--fail-on %q: want none, broke, new or changed", s.o.failOn)
		}
		return cli.Usagef("--fail-on %q: want none, error, warning or notice (or broke, new, changed when comparing)", s.o.failOn)
	}
	return nil
}

// defaultFailOn is the --fail-on level when none is given. It stands
// in for report.DefaultFailOn until that is released.
func defaultFailOn(compared bool) string {
	if compared {
		return model.Broke
	}
	return string(model.Error)
}

func cmdSnapshot(s *session) int {
	if s.o.export != "" || s.o.importFile != "" {
		return s.moveSnapshot()
	}
	if len(s.args) > 0 {
		return s.fail(usageErr("snapshot takes no arguments; actions come from %s", s.configName()))
	}
	if err := s.checkFailOn(false); err != nil {
		return s.fail(err)
	}
	if err := s.needConfig(); err != nil {
		return s.fail(err)
	}
	if err := s.selectActions(); err != nil {
		return s.fail(err)
	}
	name := cmp.Or(s.o.name, store.Stamp(time.Now()))
	dir, err := newDir(s.st, store.Incoming, name)
	if err != nil {
		return s.fail(err)
	}
	defer func() { _ = os.RemoveAll(dir) }() //nolint:gosec // our own incoming folder
	s.start(cmp.Or(s.cfg.Site, "this machine"))
	s.view.Step("Snapshot", fmt.Sprintf("%q", name))
	run, err := s.actions(name)
	if err != nil {
		return s.fail(err)
	}
	// A site that was down would replace a good baseline with one where
	// nothing was sent and the next compare would pass.
	if failed, total := failedActions(run); total > 0 && failed == total {
		if !s.o.force {
			s.allFailed = "not saved: every action failed; fix the site or pass --force"
			shown := *run
			shown.Name = "" // the summary must not say it was saved
			return s.finish(report.Result{Run: &shown}, "")
		}
		s.allFailed = "every action failed; saved anyway because of --force"
	}
	if err := store.SaveRun(dir, run); err != nil {
		return s.fail(err)
	}
	final, err := s.st.Promote(dir, name)
	if err == nil {
		err = s.st.SetLatest(name)
	}
	if err != nil {
		return s.fail(err)
	}
	s.st.ExcludeFromGit()
	return s.finish(report.Result{Run: run, Dir: final}, "next: make your change, then run: rhythm echo compare")
}

// moveSnapshot is snapshot --export and --import: they move a snapshot
// in or out of the store and run nothing, so they need no config.
func (s *session) moveSnapshot() int {
	o := s.o
	switch {
	case o.export != "" && o.importFile != "":
		return s.fail(usageErr("pick --export or --import, not both"))
	case len(s.args) > 0:
		return s.fail(usageErr("snapshot takes no arguments; name the snapshot with --name"))
	case len(o.actions) > 0 || o.failOn != "" || s.format != "" || o.open || o.quietForSet || o.timeoutSet || o.parallel != 0:
		return s.fail(usageErr("--export and --import run no actions, so they take only --name, --force, --config and --store"))
	}
	archiveErr := func(err error) int {
		if errors.Is(err, errArchive) {
			err = cli.UsageError{Msg: fmt.Sprintf("%s: %v", o.importFile, err)}
		}
		return s.fail(err)
	}
	if o.importFile != "" {
		name, err := importSnapshot(s.st, o.importFile, o.name, o.force)
		if err != nil {
			return archiveErr(err)
		}
		if !o.quiet {
			fmt.Fprintf(s.stdout, "Imported snapshot %s from %s; compare uses it now.\n", name, o.importFile)
		}
		return exitOK
	}
	dir, err := s.st.Snapshot(o.name)
	if err != nil {
		return s.fail(cli.UsageError{Msg: err.Error()})
	}
	name := filepath.Base(dir)
	write := func(w io.Writer) error { return exportSnapshot(w, dir, name) }
	if o.export == "-" {
		err = write(s.stdout)
	} else {
		err = writeFile(o.export, write)
	}
	if err != nil {
		return s.fail(err)
	}
	if o.export != "-" && !o.quiet {
		fmt.Fprintf(s.stdout, "Exported snapshot %s to %s. Elsewhere: rhythm echo compare --against %s\n", name, o.export, o.export)
	}
	return exitOK
}

func cmdCompare(s *session) int {
	if len(s.args) > 0 {
		return s.fail(usageErr("compare takes no arguments; pick a snapshot with --snapshot"))
	}
	if err := s.checkFailOn(true); err != nil {
		return s.fail(err)
	}
	if err := s.needConfig(); err != nil {
		return s.fail(err)
	}
	if s.o.against != "" && s.o.snapshot != "" {
		return s.fail(usageErr("pick --snapshot or --against, not both"))
	}
	var before *model.Run
	snapDir := s.o.against
	if snapDir != "" {
		var err error
		if before, err = loadBaseline(snapDir); err != nil {
			if errors.Is(err, errArchive) {
				err = cli.UsageError{Msg: fmt.Sprintf("--against %s: %v", snapDir, err)}
			}
			return s.fail(err)
		}
	} else {
		var err error
		if snapDir, err = s.st.Snapshot(s.o.snapshot); err != nil {
			return s.fail(cli.UsageError{Msg: err.Error()})
		}
		if before, err = store.LoadRun(snapDir); err != nil {
			return s.fail(err)
		}
	}
	if err := s.selectActions(); err != nil {
		return s.fail(err)
	}
	dir, err := newDir(s.st, "runs", store.Stamp(time.Now()))
	if err != nil {
		return s.fail(err)
	}
	s.start(cmp.Or(s.cfg.Site, before.Target, "this machine"))
	s.view.Step("Snapshot", fmt.Sprintf("%q, %s, taken %s", before.Name, plural(len(before.Actions), "action"), report.Ago(before.Started)))
	after, err := s.actions("")
	if err != nil && after == nil {
		return s.fail(err)
	}
	if err := store.SaveRun(dir, after); err != nil {
		return s.fail(err)
	}
	c := runner.CompareOptions(s.cfg).Compare(before, after, snapDir, dir, version)
	if len(s.o.accept) > 0 {
		_, unknown := compare.Accept(c, s.o.accept)
		for _, k := range unknown {
			fmt.Fprintf(s.stderr, "rhythm echo: --accept %s matched no change\n", k)
		}
	}
	c.Warnings = append(c.Warnings, after.Warnings...)
	if err := store.SaveComparison(dir, c); err != nil {
		return s.fail(err)
	}
	s.st.ExcludeFromGit()
	if err := s.st.Prune(keepRuns); err != nil {
		fmt.Fprintln(s.stderr, "rhythm echo: pruning old runs:", err)
	}
	return s.finish(report.Result{Run: after, Comparison: c, Dir: dir}, "")
}

func cmdRun(s *session) int {
	if len(s.args) > 0 {
		return s.fail(usageErr("run takes no arguments; actions come from %s (pick some with --action)", s.configName()))
	}
	if err := s.checkFailOn(false); err != nil {
		return s.fail(err)
	}
	if err := s.needConfig(); err != nil {
		return s.fail(err)
	}
	if err := s.selectActions(); err != nil {
		return s.fail(err)
	}
	dir, err := newDir(s.st, "runs", store.Stamp(time.Now()))
	if err != nil {
		return s.fail(err)
	}
	s.start(cmp.Or(s.cfg.Site, "this machine"))
	run, err := s.actions("")
	if err != nil && run == nil {
		return s.fail(err)
	}
	if err := store.SaveRun(dir, run); err != nil {
		return s.fail(err)
	}
	s.st.ExcludeFromGit()
	_ = s.st.Prune(keepRuns)
	return s.finish(report.Result{Run: run, Dir: dir}, "")
}

func cmdReport(s *session) int {
	var dir string
	var err error
	switch len(s.args) {
	case 0:
		if dir, err = s.st.LatestRun(); err != nil {
			err = cli.UsageError{Msg: err.Error()}
		}
	case 1:
		if fi, statErr := os.Stat(s.args[0]); statErr == nil && fi.IsDir() { //nolint:gosec // the user names a folder
			dir = s.args[0]
		} else if dir, err = s.st.Snapshot(s.args[0]); err != nil {
			err = cli.UsageError{Msg: err.Error()}
		}
	default:
		err = usageErr("report takes at most one run folder or snapshot name")
	}
	if err != nil {
		return s.fail(err)
	}
	run, err := store.LoadRun(dir)
	if err != nil {
		return s.fail(err)
	}
	res := report.Result{Run: run, Dir: dir}
	if c, err := store.LoadComparison(dir); err == nil {
		res.Comparison = c
	}
	if err := s.checkFailOn(res.Compared()); err != nil {
		return s.fail(err)
	}
	s.view.Header(version, cmp.Or(run.Target, "this machine"))
	return s.finish(res, "")
}

// cmdWatch prints messages as they arrive at a running Echo, the
// problems found in each and a line when one changes status (an
// observed request gets its answer, a check blocks it). With --json it
// writes one JSON object per line instead: {"type": "message" or
// "update", "message": {...}}.
func cmdWatch(s *session) int {
	if len(s.args) > 0 {
		return s.fail(usageErr("watch takes no arguments; narrow it with --run, --to or --channel"))
	}
	h, err := s.echo.Health(s.ctx)
	if err != nil {
		return s.fail(err)
	}
	if !s.o.quiet {
		fmt.Fprintf(s.stderr, "Watching %s (%s mode). Ctrl+C to stop.\n", s.echo.URL, h.Mode)
	}
	w := &watcher{s: s, status: newStatusLRU(watchMemory), enc: json.NewEncoder(s.stdout), opts: s.checks}
	w.enc.SetEscapeHTML(false)
	start := time.Now().UTC()
	if s.o.since > 0 {
		start = start.Add(-s.o.since)
	}
	newest, known := start, 0
	for s.ctx.Err() == nil {
		// Status changes keep a message's time, so each poll asks again
		// for the last few minutes: that's where updates happen. Asking
		// for one more message than last time returns as soon as a new
		// one arrives and after a second otherwise, to show updates.
		since := newest.Add(-watchWindow)
		if since.Before(start) {
			since = start
		}
		ms, err := s.echo.Wait(s.ctx, s.filter(since), known+1, 200*time.Millisecond, time.Second)
		if err != nil {
			if s.ctx.Err() != nil {
				break
			}
			return s.fail(err)
		}
		known = len(ms)
		for _, m := range ms {
			if err := w.show(m); err != nil {
				return s.fail(err)
			}
			if m.At.After(newest) {
				newest = m.At
			}
		}
	}
	return exitOK
}

const (
	watchMemory = 20000           // statuses watch remembers; the inbox keeps 10000 messages
	watchWindow = 2 * time.Minute // how far back watch looks for status changes
)

// watcher prints each message once and again as an update when its
// status changes.
type watcher struct {
	s      *session
	status *statusLRU
	enc    *json.Encoder
	opts   check.Options
}

// watchLine is one line of watch --json.
type watchLine struct {
	Type    string        `json:"type"` // message or update
	Message model.Message `json:"message"`
	Was     model.Status  `json:"was,omitempty"`    // update: the status before
	Issues  []model.Issue `json:"issues,omitempty"` // message: problems found in it
}

func (w *watcher) show(m model.Message) error {
	prev, seen := w.status.put(m.ID, m.Status)
	switch {
	case !seen:
		issues := check.Message(m, w.opts)
		if w.s.o.json {
			return w.enc.Encode(watchLine{Type: "message", Message: m, Issues: issues})
		}
		w.s.view.Message(m)
		printIssues(w.s.stdout, issues, w.s.plain)
	case prev != m.Status:
		if w.s.o.json {
			return w.enc.Encode(watchLine{Type: "update", Message: m, Was: prev})
		}
		w.s.view.Step("Update", updateLine(m, prev))
	}
	return nil
}

// updateLine says what changed, in the words up's live log uses.
func updateLine(m model.Message, prev model.Status) string {
	what := check.Describe(m) // names the host for webhooks and requests
	if c := m.Channel; len(m.To) > 0 && (c == model.Email || c == model.SMS || c == model.WhatsApp) {
		what += " to " + strings.Join(m.To, ", ")
	}
	return fmt.Sprintf("%s %s is now %s (was %s)", m.At.Local().Format("15:04:05"), what, m.Status, prev)
}

// printIssues lists the problems in a message under its line.
func printIssues(w io.Writer, issues []model.Issue, plain bool) {
	for _, i := range issues {
		if plain {
			fmt.Fprintf(w, "  [%s] %s\n", strings.ToUpper(string(i.Level)), i.Title)
			continue
		}
		mark := lipgloss.NewStyle().Foreground(report.LevelColor(i.Level)).Bold(true).Render("! " + string(i.Level))
		fmt.Fprintf(w, "            %s  %s\n", mark, i.Title)
	}
}

// statusLRU remembers the last status of the most recently seen
// messages, forgetting the least recently seen beyond max, so a long
// watch doesn't grow without bound.
type statusLRU struct {
	max   int
	order *list.List // of string IDs, most recent first
	at    map[string]*list.Element
	value map[string]model.Status
}

func newStatusLRU(max int) *statusLRU {
	return &statusLRU{max: max, order: list.New(), at: map[string]*list.Element{}, value: map[string]model.Status{}}
}

// put records id's status and returns the one before, if id was known.
func (c *statusLRU) put(id string, st model.Status) (prev model.Status, seen bool) {
	if e, ok := c.at[id]; ok {
		prev, seen = c.value[id], true
		c.order.MoveToFront(e)
	} else {
		c.at[id] = c.order.PushFront(id)
	}
	c.value[id] = st
	for c.order.Len() > c.max {
		old := c.order.Remove(c.order.Back()).(string) //nolint:forcetypeassert // the list only holds IDs
		delete(c.at, old)
		delete(c.value, old)
	}
	return prev, seen
}

func (s *session) filter(since time.Time) client.Filter {
	return client.Filter{Run: s.o.runID, Channel: model.Channel(s.o.channel), To: s.o.to, Since: since}
}

func cmdList(s *session) int {
	if len(s.args) > 0 {
		return s.fail(usageErr("ls takes no arguments; narrow it with --run, --to, --channel or --since (rhythm echo show <id> for one)"))
	}
	since := time.Time{}
	if s.o.since > 0 {
		since = time.Now().UTC().Add(-s.o.since)
	}
	ms, err := s.echo.Messages(s.ctx, s.filter(since))
	if err != nil {
		return s.fail(err)
	}
	if s.o.json {
		return jsonOut(s, kindMessages, ms)
	}
	if len(ms) == 0 && !s.o.quiet {
		fmt.Fprintln(s.stderr, "No messages captured yet.")
	}
	for _, m := range ms {
		fmt.Fprintf(s.stdout, "%s  ", m.ID)
		s.view.Message(m)
	}
	return exitOK
}

func cmdShow(s *session) int {
	if len(s.args) != 1 {
		return s.fail(usageErr("show needs a message ID from: rhythm echo ls"))
	}
	m, err := s.echo.Message(s.ctx, s.args[0])
	if client.IsNotFound(err) {
		return s.fail(usageErr("no message %q in Echo; list them with: rhythm echo ls", s.args[0]))
	}
	if err != nil {
		return s.fail(err)
	}
	return jsonOut(s, kindMessages, []model.Message{m})
}

func cmdClear(s *session) int {
	// "clear <id>" would otherwise delete every message.
	if len(s.args) > 0 {
		return s.fail(usageErr("clear takes no arguments; narrow it with --run, --to or --channel"))
	}
	n, err := s.echo.Clear(s.ctx, s.filter(time.Time{}))
	if err != nil {
		return s.fail(err)
	}
	fmt.Fprintf(s.stdout, "Deleted %s\n", plural(n, "message"))
	return exitOK
}

// cmdSnapshots lists the snapshots in the store, or deletes one.
func cmdSnapshots(s *session) int {
	st, args := s.st, s.args
	if len(args) == 2 && (args[0] == "delete" || args[0] == "rm") {
		if _, err := st.Snapshot(args[1]); err != nil {
			return s.fail(cli.UsageError{Msg: err.Error()})
		}
		before, _ := st.Snapshot("")
		if err := st.Delete(args[1]); err != nil {
			return s.fail(err)
		}
		fmt.Fprintln(s.stdout, "Deleted snapshot", args[1])
		if after, err := st.Snapshot(""); err == nil && after != before {
			fmt.Fprintln(s.stdout, "The latest snapshot is now", filepath.Base(after))
		}
		return exitOK
	}
	if len(args) > 0 {
		return s.fail(usageErr("usage: rhythm echo snapshots [delete <name>]"))
	}
	names, err := st.Snapshots()
	if err != nil {
		return s.fail(err)
	}
	latest, _ := st.Snapshot("")
	if s.o.json {
		entries := make([]snapshotEntry, 0, len(names))
		for _, n := range names {
			dir := filepath.Join(st.Root, "snapshots", n)
			r, err := store.LoadRun(dir)
			if err != nil {
				continue // the table says why; JSON lists what compare can use
			}
			e := snapshotEntry{Name: n, Started: r.Started, Actions: len(r.Actions), Latest: dir == latest}
			for _, a := range r.Actions {
				e.Messages += len(a.Messages)
			}
			entries = append(entries, e)
		}
		return jsonOut(s, kindSnapshots, entries)
	}
	if len(names) == 0 {
		fmt.Fprintln(s.stdout, "No snapshots yet. Take one: rhythm echo snapshot")
		return exitOK
	}
	fmt.Fprintf(s.stdout, "%-24s %-8s %-12s %s\n", "NAME", "ACTIONS", "TAKEN", "SENT")
	for _, n := range names {
		dir := filepath.Join(st.Root, "snapshots", n)
		r, err := store.LoadRun(dir)
		if err != nil {
			fmt.Fprintf(s.stdout, "%-24s %s\n", n, err)
			continue
		}
		mark := ""
		if dir == latest {
			mark = "  (latest)"
		}
		fmt.Fprintf(s.stdout, "%-24s %-8d %-12s %s%s\n", n, len(r.Actions), report.Ago(r.Started), report.Tally(r), mark)
	}
	return exitOK
}

// initUsage is init's help and its usage line.
const initUsage = `usage: rhythm echo init [--stack s] [--force]
  --stack s   wordpress, laravel, next, node or php (default: from this folder's files)
  --force     replace an existing .echo/config.yaml`

// cmdInit writes .echo/config.yaml with one action for the project's
// stack, so the first snapshot has something to run.
func cmdInit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	force := fs.Bool("force", false, "")
	stackFlag := fs.String("stack", "", "")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stdout, initUsage)
			return exitOK
		}
		if err != nil {
			fmt.Fprintln(stderr, "rhythm echo:", ownFlagError(err, fs))
		}
		fmt.Fprintln(stderr, initUsage)
		return exitUsage
	}
	stack := detectStack(".")
	if *stackFlag != "" {
		var ok bool
		if stack, ok = stackNames[strings.ToLower(*stackFlag)]; !ok {
			fmt.Fprintf(stderr, "rhythm echo: --stack %q: use wordpress, laravel, next, node or php\n", *stackFlag)
			return exitUsage
		}
	}
	path := config.Path()
	if _, err := os.Stat(path); err == nil && !*force {
		fmt.Fprintf(stderr, "rhythm echo: %s already exists (--force replaces it)\n", path)
		return exitUsage
	}
	if above := configAbove(); above != "" {
		// Commands use the nearest config, which will be this new one.
		fmt.Fprintf(stderr, "rhythm echo: note: %s is above this folder; here the new one takes its place\n", above)
	}
	// .echo/ will hold captured messages, so only its owner may open it.
	if err := os.MkdirAll(config.Dir, 0o700); err != nil {
		fmt.Fprintln(stderr, "rhythm echo:", err)
		return exitError
	}
	st := starterFor(stack, ".")
	if err := os.WriteFile(path, []byte(initConfig(st)), 0o644); err != nil { //nolint:gosec // a project file meant to be committed
		fmt.Fprintln(stderr, "rhythm echo:", err)
		return exitError
	}
	store.Store{Root: config.Dir}.ExcludeFromGit()
	if stack == "" {
		fmt.Fprintf(stdout, "Wrote %s with one action to start from (%s).\n", path, st.name)
	} else {
		fmt.Fprintf(stdout, "Wrote %s for a %s site, with one action to start from (%s).\n", path, stackLabel(stack), st.name)
	}
	switch {
	case st.needSite && st.site == "":
		fmt.Fprintf(stdout, "Set site: in it to your staging site's URL; the action's URL is relative to it.\n")
	case st.site != "":
		fmt.Fprintf(stdout, "site: is %s; change it if your staging site is elsewhere.\n", st.site)
	}
	fmt.Fprintln(stdout, "Then: rhythm echo up, rhythm echo doctor and rhythm echo snapshot.")
	return exitOK
}

// configAbove is the config commands use here before init writes one:
// the nearest above the working folder, within the same search
// findConfig makes. "" when there's none, or the working folder is
// where that search ends.
func configAbove() string {
	if _, err := os.Lstat(".git"); err == nil {
		return ""
	}
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && filepath.Clean(home) == wd {
		return ""
	}
	path, _ := findConfig(filepath.Dir(wd))
	return path
}

// cmdUpdate exists so `rhythm update` (which runs every tool's update)
// succeeds; Echo has no data to refresh.
func cmdUpdate(_ []string, stdout, _ io.Writer) int {
	fmt.Fprintln(stdout, "Echo has nothing to refresh; provider recognizers update with the binary (rhythm update).")
	return exitOK
}

// jsonOut writes v to stdout in an envelope of kind.
func jsonOut(s *session, kind jsonKind, v any) int {
	if err := writeEnvelope(s.stdout, kind, v); err != nil {
		return s.fail(err)
	}
	return exitOK
}

// newDir is st.NewDir with the folder and the one it sits in (runs or
// snapshots) made 0700, as writeFile's folders are: the store makes them
// 0750 and its files hold captured messages. Promote moves the folder
// whole, so a snapshot keeps the mode.
func newDir(st store.Store, kind, name string) (string, error) {
	dir, err := st.NewDir(kind, name)
	if err != nil {
		return "", err
	}
	for _, d := range []string{filepath.Dir(dir), dir} {
		if err := os.Chmod(d, 0o700); err != nil { //nolint:gosec // a folder: 0700 lets only its owner list it
			return "", err
		}
	}
	return dir, nil
}

// writeFile writes a report atomically, readable only by its owner: a
// report holds the captured messages themselves (recipients, bodies), so
// it gets the same 0600 as run.json and a new folder gets 0700.
func writeFile(path string, write func(io.Writer) error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".report-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := write(tmp); err != nil {
		_ = tmp.Close()
		return err
	}
	// CreateTemp made the file 0600, which the rename keeps.
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func openFile(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	// The commands are fixed; the argument is the report just written.
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", abs) //nolint:gosec // see above
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", abs) //nolint:gosec // see above
	default:
		c = exec.Command("xdg-open", abs) //nolint:gosec // see above
	}
	return c.Start()
}

func rel(path string) string {
	wd, err := os.Getwd()
	if err != nil {
		return path
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if r, err := filepath.Rel(wd, abs); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return path
}

// chdir changes into dir and returns what changes back.
func chdir(dir string) (func(), error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	if err := os.Chdir(dir); err != nil {
		return nil, err
	}
	return func() { _ = os.Chdir(wd) }, nil
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// jsonKind says what a JSON envelope holds. The kinds, the envelope and
// its counts stand in for report.Kind, report.Envelope and
// report.WriteJSON until those are released, with the same fields.
type jsonKind string

// The kinds of --json output, one per command family. replay is the
// CLI's own: the report package has no replay kind yet.
const (
	kindRun        jsonKind = "run"        // run, snapshot: the run with its actions
	kindComparison jsonKind = "comparison" // compare, report on a comparison
	kindCheck      jsonKind = "check"      // check <domain>: the DNS report
	kindHeaders    jsonKind = "headers"    // headers <file.eml>
	kindMessages   jsonKind = "messages"   // ls, show: captured messages
	kindSnapshots  jsonKind = "snapshots"  // snapshots: the stored snapshots
	kindDoctor     jsonKind = "doctor"     // doctor: what was checked and how it went
	kindReplay     jsonKind = "replay"     // replay: the site's answer and what it sent
	kindLatest     jsonKind = "latest"     // latest: the newest message with its codes and links
)

// The verdicts of an envelope. They don't depend on --fail-on, which
// decides only the exit code.
const (
	verdictOK       = "ok"       // nothing found
	verdictFindings = "findings" // changes or issues
	verdictError    = "error"    // part couldn't be checked: an action failed, DNS didn't answer
)

// envelope is the one JSON shape every command's --json writes: what it
// is, counts and a verdict that read the same for every kind, then the
// command's own data.
type envelope struct {
	Schema   string      `json:"schema"`
	Kind     jsonKind    `json:"kind"`
	Tool     string      `json:"tool"`
	Version  string      `json:"version"`
	Complete bool        `json:"complete"` // false when part of it couldn't be checked (the verdict is then error)
	Summary  jsonSummary `json:"summary"`
	Verdict  string      `json:"verdict"`
	Data     any         `json:"data"`
}

// jsonSummary counts what an envelope holds. A count that doesn't apply
// to the kind is 0.
type jsonSummary struct {
	Broke         int `json:"broke"`
	New           int `json:"new"`
	Changed       int `json:"changed"`
	Fixed         int `json:"fixed"`
	Errors        int `json:"errors"`
	Warnings      int `json:"warnings"`
	Notices       int `json:"notices"`
	Actions       int `json:"actions"` // run: actions run; comparison: actions compared
	Messages      int `json:"messages"`
	FailedActions int `json:"failed_actions"`
}

// snapshotEntry is one snapshot in snapshots --json.
type snapshotEntry struct {
	Name     string    `json:"name"`
	Started  time.Time `json:"started"`
	Actions  int       `json:"actions"`
	Messages int       `json:"messages"`
	Latest   bool      `json:"latest"` // the snapshot compare uses when none is named
}

// doctorCheck is one line of doctor --json. Detail says what to do
// about a failed check.
type doctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok, warning or error
	Detail string `json:"detail,omitempty"`
}

// doctorChecks is what doctor --json writes: the checks and whether
// doctor stopped early (Echo couldn't be reached, so nothing else was
// checked), which makes the envelope incomplete.
type doctorChecks struct {
	Checks  []doctorCheck
	Stopped bool
}

// replayData is replay --json's data.
type replayData struct {
	Replay   replay.Result   `json:"replay"`
	Messages []model.Message `json:"messages"`
}

// comparisonData is a comparison as an envelope holds it: "actions" is
// the compared run's actions, as in a run; their count is in the
// summary.
type comparisonData struct {
	*model.Comparison
	Actions []model.Action `json:"actions"`
}

// newEnvelope wraps v as kind and counts what it holds.
func newEnvelope(kind jsonKind, v any) envelope {
	e := envelope{Schema: model.Schema, Kind: kind, Tool: "rhythm-echo", Version: version, Complete: true, Data: v}
	var issues []model.Issue
	switch t := v.(type) {
	case report.Result:
		if t.Compared() {
			c := t.Comparison
			e.Version = cmp.Or(c.Version, e.Version)
			e.Summary.Broke, e.Summary.New = c.Count(model.Broke), c.Count(model.New)
			e.Summary.Changed, e.Summary.Fixed = c.Count(model.Changed), c.Count(model.Fixed)
			e.Summary.Actions = c.Actions
			var actions []model.Action
			if t.Run != nil {
				actions = t.Run.Actions
			}
			e.Data = comparisonData{Comparison: c, Actions: nonNil(actions)}
		}
		if t.Run != nil {
			e.addRun(t.Run, !t.Compared())
			issues = report.Issues(t.Run)
			if !t.Compared() {
				e.Data = t.Run
			}
		}
	case unreachableReport:
		issues = t.Issues
		e.Complete = !t.Unreachable
	case dns.Report:
		issues = t.Issues
	case headers.Result:
		issues = t.Issues
	case []model.Message:
		e.Summary.Messages = len(t)
		e.Data = nonNil(t)
	case replayData:
		e.Summary.Messages = len(t.Messages)
		t.Messages = nonNil(t.Messages)
		e.Data = t
	case latestResult:
		e.Summary.Messages = 1
		t.Codes, t.Links = nonNil(t.Codes), nonNil(t.Links)
		e.Data = t
	case []snapshotEntry:
		e.Data = nonNil(t)
	case doctorChecks:
		e.Complete = !t.Stopped
		for _, c := range t.Checks {
			switch c.Status {
			case string(model.Error):
				e.Summary.Errors++
			case string(model.Warning):
				e.Summary.Warnings++
			}
		}
		e.Data = nonNil(t.Checks)
	}
	if len(issues) > 0 {
		n := report.Counts(issues)
		e.Summary.Errors, e.Summary.Warnings, e.Summary.Notices = n[model.Error], n[model.Warning], n[model.Notice]
	}
	s := e.Summary
	switch {
	case !e.Complete:
		e.Verdict = verdictError
	case s.Broke+s.New+s.Changed+s.Errors+s.Warnings+s.Notices > 0:
		e.Verdict = verdictFindings
	default:
		e.Verdict = verdictOK
	}
	return e
}

// addRun counts a run's actions and messages; a failed action or a
// payload cut to size makes the result incomplete. actions says whether
// the run's action count is the summary's (a comparison counts the
// actions it compared instead).
func (e *envelope) addRun(r *model.Run, actions bool) {
	e.Version = cmp.Or(r.Version, e.Version)
	if actions && r.Mode != "check" { // check and headers run no actions
		e.Summary.Actions = len(r.Actions)
	}
	failed, _ := failedActions(r)
	e.Summary.FailedActions = failed
	for _, a := range r.Actions {
		e.Summary.Messages += len(a.Messages)
		for _, m := range a.Messages {
			if strings.HasSuffix(m.Body, "…(truncated)") {
				e.Complete = false
			}
		}
	}
	if failed > 0 {
		e.Complete = false
	}
}

// writeEnvelope writes v in an envelope of kind, indented as
// report.JSONValue does.
func writeEnvelope(w io.Writer, kind jsonKind, v any) error {
	return report.JSONValue(w, newEnvelope(kind, v))
}

// resultKind is the kind of a run or comparison result.
func resultKind(res report.Result) jsonKind {
	if res.Compared() {
		return kindComparison
	}
	return kindRun
}

// nonNil makes an empty list encode as [] rather than null.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
