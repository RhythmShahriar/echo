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
	"net/url"
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

// command parses flags and runs fn, turning errors into exit codes.
func command(fn func(*session) int, args []string, stdout, stderr io.Writer) int {
	p, err := parse(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			usage(stdout)
			return exitOK
		}
		fmt.Fprintln(stderr, "rhythm echo:", err)
		return exitUsage
	}
	warnTokenFlag(stderr, p.o.token, p.o.quiet)
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
	return errors.As(err, &ue) || errors.Is(err, runner.ErrNoActions) || errors.Is(err, runner.ErrUnknownAction) ||
		errors.Is(err, replay.ErrNoSecret) || errors.Is(err, replay.ErrRefused) || errors.Is(err, replay.ErrPayPal)
}

// selectActions checks the actions to run before anything is printed.
func (s *session) selectActions() error {
	_, err := runner.Select(s.cfg, s.o.actions)
	return err
}

// start prints the header and the one-time notice, and checks for a new
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
	if h, err := s.echo.Health(s.ctx); err != nil {
		return nil, err
	} else {
		s.view.Step("Echo", fmt.Sprintf("%s mode at %s", h.Mode, s.echo.URL))
	}
	s.ran = true
	return runner.Run(s.ctx, runner.Options{Config: s.cfg, Client: s.echo, Version: version, Name: name, Only: s.o.actions,
		Replay: replayer(s.cfg), Hooks: runner.Hooks{Action: s.view.Action, Log: s.view.Log}})
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
				return report.JSON(w, res)
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

func (s *session) checkFailOn(compared bool) error {
	if s.o.failOn == "" || s.o.failOn == failOnNone {
		return nil
	}
	if err := report.ValidFailOn(s.o.failOn, compared); err != nil {
		return cli.UsageError{Msg: err.Error()}
	}
	return nil
}

func cmdSnapshot(s *session) int {
	if len(s.args) > 0 {
		return s.fail(usageErr("snapshot takes no arguments; actions come from %s", config.Path()))
	}
	if err := s.checkFailOn(false); err != nil {
		return s.fail(err)
	}
	if err := s.selectActions(); err != nil {
		return s.fail(err)
	}
	name := cmp.Or(s.o.name, store.Stamp(time.Now()))
	dir, err := s.st.NewDir(store.Incoming, name)
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

func cmdCompare(s *session) int {
	if len(s.args) > 0 {
		return s.fail(usageErr("compare takes no arguments; pick a snapshot with --snapshot"))
	}
	if err := s.checkFailOn(true); err != nil {
		return s.fail(err)
	}
	snapDir, err := s.st.Snapshot(s.o.snapshot)
	if err != nil {
		return s.fail(cli.UsageError{Msg: err.Error()})
	}
	if err := s.selectActions(); err != nil {
		return s.fail(err)
	}
	before, err := store.LoadRun(snapDir)
	if err != nil {
		return s.fail(err)
	}
	dir, err := s.st.NewDir("runs", store.Stamp(time.Now()))
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
	c := compare.Compare(before, after, snapDir, dir, version)
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
		return s.fail(usageErr("run takes no arguments; actions come from %s (pick some with --action)", config.Path()))
	}
	if err := s.checkFailOn(false); err != nil {
		return s.fail(err)
	}
	if err := s.selectActions(); err != nil {
		return s.fail(err)
	}
	dir, err := s.st.NewDir("runs", store.Stamp(time.Now()))
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
	h, err := s.echo.Health(s.ctx)
	if err != nil {
		return s.fail(err)
	}
	if !s.o.quiet {
		fmt.Fprintf(s.stderr, "Watching %s (%s mode). Ctrl+C to stop.\n", s.echo.URL, h.Mode)
	}
	w := &watcher{s: s, status: newStatusLRU(watchMemory), enc: json.NewEncoder(s.stdout)}
	w.enc.SetEscapeHTML(false)
	if u, err := url.Parse(s.cfg.Site); err == nil {
		w.opts.SiteHost = u.Hostname()
	}
	w.opts.AllowRecipients = s.cfg.AllowRecipients
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
	since := time.Time{}
	if s.o.since > 0 {
		since = time.Now().UTC().Add(-s.o.since)
	}
	ms, err := s.echo.Messages(s.ctx, s.filter(since))
	if err != nil {
		return s.fail(err)
	}
	if s.o.json {
		return jsonOut(s, ms)
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
	return jsonOut(s, m)
}

func cmdClear(s *session) int {
	n, err := s.echo.Clear(s.ctx, s.filter(time.Time{}))
	if err != nil {
		return s.fail(err)
	}
	fmt.Fprintf(s.stdout, "Deleted %s\n", plural(n, "message"))
	return exitOK
}

func cmdSnapshots(args []string, stdout, stderr io.Writer) int {
	st := store.Store{Root: config.Dir}
	if len(args) == 2 && (args[0] == "delete" || args[0] == "rm") {
		if _, err := st.Snapshot(args[1]); err != nil {
			fmt.Fprintln(stderr, "rhythm echo:", err)
			return exitUsage
		}
		before, _ := st.Snapshot("")
		if err := st.Delete(args[1]); err != nil {
			fmt.Fprintln(stderr, "rhythm echo:", err)
			return exitError
		}
		fmt.Fprintln(stdout, "Deleted snapshot", args[1])
		if after, err := st.Snapshot(""); err == nil && after != before {
			fmt.Fprintln(stdout, "The latest snapshot is now", filepath.Base(after))
		}
		return exitOK
	}
	if len(args) > 0 {
		fmt.Fprintln(stderr, "rhythm echo: usage: rhythm echo snapshots [delete <name>]")
		return exitUsage
	}
	names, err := st.Snapshots()
	if err != nil {
		fmt.Fprintln(stderr, "rhythm echo:", err)
		return exitError
	}
	if len(names) == 0 {
		fmt.Fprintln(stdout, "No snapshots yet. Take one: rhythm echo snapshot")
		return exitOK
	}
	latest, _ := st.Snapshot("")
	fmt.Fprintf(stdout, "%-24s %-8s %-12s %s\n", "NAME", "ACTIONS", "TAKEN", "SENT")
	for _, n := range names {
		dir := filepath.Join(st.Root, "snapshots", n)
		r, err := store.LoadRun(dir)
		if err != nil {
			fmt.Fprintf(stdout, "%-24s %s\n", n, err)
			continue
		}
		mark := ""
		if dir == latest {
			mark = "  (latest)"
		}
		fmt.Fprintf(stdout, "%-24s %-8d %-12s %s%s\n", n, len(r.Actions), report.Ago(r.Started), report.Tally(r), mark)
	}
	return exitOK
}

func cmdInit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	force := fs.Bool("force", false, "")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stdout, "usage: rhythm echo init [--force]   (--force replaces an existing config)")
			return exitOK
		}
		fmt.Fprintln(stderr, "rhythm echo: usage: rhythm echo init [--force]")
		return exitUsage
	}
	path := config.Path()
	if _, err := os.Stat(path); err == nil && !*force {
		fmt.Fprintf(stderr, "rhythm echo: %s already exists (--force replaces it)\n", path)
		return exitUsage
	}
	// .echo/ will hold captured messages, so only its owner may open it.
	if err := os.MkdirAll(config.Dir, 0o700); err != nil {
		fmt.Fprintln(stderr, "rhythm echo:", err)
		return exitError
	}
	if err := os.WriteFile(path, []byte(config.Template), 0o644); err != nil { //nolint:gosec // a project file meant to be committed
		fmt.Fprintln(stderr, "rhythm echo:", err)
		return exitError
	}
	store.Store{Root: config.Dir}.ExcludeFromGit()
	fmt.Fprintf(stdout, "Wrote %s. Every setting is optional; add actions to snapshot and compare.\n", path)
	return exitOK
}

// cmdUpdate exists so `rhythm update` (which runs every tool's update)
// succeeds; Echo has no data to refresh.
func cmdUpdate(_ []string, stdout, _ io.Writer) int {
	fmt.Fprintln(stdout, "Echo has nothing to refresh; provider recognizers update with the binary (rhythm update).")
	return exitOK
}

func jsonOut(s *session, v any) int {
	if err := report.JSONValue(s.stdout, v); err != nil {
		return s.fail(err)
	}
	return exitOK
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

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
