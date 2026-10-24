// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/runner"
	"github.com/RhythmShahriar/echo/store"
	"github.com/RhythmShahriar/rhythm/kit/cache"
)

// Schedule is when an update window opens: on some days of the week at a
// time of day, in the clock's own time zone.
type Schedule struct {
	Spec   string
	days   [7]bool // by time.Weekday
	hour   int
	minute int
}

var (
	clockRe = regexp.MustCompile(`^([01]?\d|2[0-3]):([0-5]\d)$`)
	dayName = map[string]time.Weekday{"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
		"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday}
)

// ParseSchedule reads "Tue 02:00", "Mon,Thu 03:30", "daily 02:00",
// "weekdays 22:00", "weekends 06:00" or a bare "02:00" (every day). Day
// names take any case, short or long ("tuesday").
func ParseSchedule(spec string) (Schedule, error) {
	s := Schedule{Spec: spec}
	fields := strings.Fields(spec)
	bad := func() (Schedule, error) {
		return Schedule{}, fmt.Errorf(`schedule %q: want days and a time, like "Tue 02:00", "Mon,Thu 03:30", "daily 02:00" or "weekdays 22:00"`, spec)
	}
	if len(fields) == 0 || len(fields) > 2 {
		return bad()
	}
	m := clockRe.FindStringSubmatch(fields[len(fields)-1])
	if m == nil {
		return bad()
	}
	s.hour, _ = strconv.Atoi(m[1])
	s.minute, _ = strconv.Atoi(m[2])
	days := "daily"
	if len(fields) == 2 {
		days = strings.ToLower(fields[0])
	}
	switch days {
	case "daily", "everyday", "every-day":
		s.days = [7]bool{true, true, true, true, true, true, true}
	case "weekdays":
		for d := time.Monday; d <= time.Friday; d++ {
			s.days[d] = true
		}
	case "weekends":
		s.days[time.Saturday], s.days[time.Sunday] = true, true
	default:
		for _, d := range strings.Split(days, ",") {
			d = strings.TrimSpace(d)
			if len(d) < 3 {
				return bad()
			}
			wd, ok := dayName[d[:3]]
			if !ok || !strings.HasPrefix(strings.ToLower(wd.String()), d) {
				return bad()
			}
			s.days[wd] = true
		}
	}
	return s, nil
}

// Next returns the first window strictly after t, in t's time zone. A
// time that a daylight saving change skips that day moves to the next
// matching day.
func (s Schedule) Next(t time.Time) time.Time {
	for i := 0; i <= 8; i++ {
		d := t.AddDate(0, 0, i)
		if !s.days[d.Weekday()] {
			continue
		}
		at := time.Date(d.Year(), d.Month(), d.Day(), s.hour, s.minute, 0, 0, t.Location())
		if at.Hour() != s.hour || at.Minute() != s.minute {
			continue // skipped by a clock change
		}
		if at.After(t) {
			return at
		}
	}
	return time.Time{} // unreachable: some day in the next eight matches
}

// WindowOptions configure one update window.
type WindowOptions struct {
	Store        string        // the hub's store: results go under sites/<id>
	Sites        []*Site       // in order
	URL, Token   string        // the hub's API and its read token
	HTTP         *http.Client  // nil: a default client (set one that trusts a self-signed hub)
	After        string        // the update command, run with sh -c in each site's folder
	Hide         []string      // variables After must not see: Sites.SecretEnv
	AfterTimeout time.Duration // default 30 minutes
	Snapshot     string        // compare with this snapshot; empty takes a fresh one before After
	Version      string
	Now          func() time.Time
	Log          io.Writer // progress and the command's output; nil discards
}

// Result is what one site's window found. It is kept as schedule.json in
// the compare's run folder, for History.
type Result struct {
	Site       string         `json:"site"`
	Started    time.Time      `json:"started"`
	Finished   time.Time      `json:"finished"`
	After      string         `json:"after,omitempty"`
	AfterError string         `json:"after_error,omitempty"`
	Snapshot   string         `json:"snapshot,omitempty"`
	Dir        string         `json:"dir,omitempty"`
	Verdict    string         `json:"verdict,omitempty"`
	Counts     map[string]int `json:"counts,omitempty"`
	Error      string         `json:"error,omitempty"` // the window couldn't compare
}

// Broke reports whether the site broke or couldn't be checked.
func (r Result) Broke() bool { return r.Error != "" || r.AfterError != "" || r.Counts[model.Broke] > 0 }

// Replay, when set, makes the replayer for a site's replay actions, given
// the site's config, its project folder and its config file (the
// replayer looks there for live payment keys). The CLI sets it before
// calling ScheduleMain; without it replay actions report that replay
// isn't available.
var Replay func(cfg config.Config, dir, cfgPath string) runner.Replayer

// preSnapshot is the snapshot each window takes before its update. One
// name per site, replaced each time, so windows don't pile up snapshots.
const preSnapshot = "pre-update"

// keepWindows is how many compare runs each site keeps: two years of
// weekly windows.
const keepWindows = 104

// SiteStore is where a site's snapshots and runs live in the hub's store.
func SiteStore(hubStore, site string) store.Store {
	return store.Store{Root: filepath.Join(hubStore, "sites", site)}
}

// RunWindow runs one update window, a site at a time (they share the
// hub's inbox): a snapshot unless Snapshot names one, the After command,
// then a compare, stored with its result. A site that fails is recorded
// and the next one still runs.
func RunWindow(ctx context.Context, o WindowOptions) []Result {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.AfterTimeout <= 0 {
		o.AfterTimeout = 30 * time.Minute
	}
	if o.Log == nil {
		o.Log = io.Discard
	}
	var out []Result
	for _, site := range o.Sites {
		if ctx.Err() != nil {
			break
		}
		r := window(ctx, o, siteClient(o, site.ID), site)
		out = append(out, r)
		line := r.Verdict
		if r.Error != "" {
			line = "couldn't compare: " + r.Error
		}
		if r.AfterError != "" {
			line += " (the update failed: " + r.AfterError + ")"
		}
		fmt.Fprintf(o.Log, "%s: %s\n", site.ID, line)
	}
	return out
}

// siteClient talks to the hub with the read token, naming site in every
// request so the hub keeps the run's action markers to that site's mail.
func siteClient(o WindowOptions, site string) *client.Client {
	echo := client.New(o.URL, o.Token)
	hc := echo.HTTP // the client's own, with its timeout
	if o.HTTP != nil {
		hc = o.HTTP
	}
	base := hc.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	named := *hc
	named.Transport = siteTransport{base: base, site: site}
	echo.HTTP = &named
	return echo
}

// siteTransport sets SiteHeader on each request.
type siteTransport struct {
	base http.RoundTripper
	site string
}

func (t siteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set(SiteHeader, t.site)
	return t.base.RoundTrip(r)
}

func window(ctx context.Context, o WindowOptions, echo *client.Client, site *Site) Result {
	r := Result{Site: site.ID, Started: o.Now().UTC(), After: o.After}
	fail := func(err error) Result {
		r.Error, r.Finished = err.Error(), o.Now().UTC()
		return r
	}
	if site.Config == "" {
		return fail(errors.New("sites.yaml gives it no config"))
	}
	cfg, err := config.Load(site.Config)
	if err != nil {
		return fail(err)
	}
	st := SiteStore(o.Store, site.ID)
	// The site's actions run in its project folder, as they do from
	// there with rhythm echo snapshot: "./send.sh" or "wp cron event run"
	// must reach this site, not whatever the hub runs in.
	runOpts := runner.Options{Config: cfg, Client: echo, Version: o.Version, Dir: site.ProjectDir()}
	if Replay != nil {
		runOpts.Replay = Replay(cfg, site.ProjectDir(), site.Config)
	}

	r.Snapshot = o.Snapshot
	var snapDir string
	if o.Snapshot == "" {
		r.Snapshot = preSnapshot
		fmt.Fprintf(o.Log, "%s: snapshot before the update\n", site.ID)
		if snapDir, err = snapshot(ctx, st, runOpts); err != nil {
			return fail(fmt.Errorf("snapshot: %w", err))
		}
	} else if snapDir, err = st.Snapshot(o.Snapshot); err != nil {
		return fail(err)
	}
	before, err := store.LoadRun(snapDir)
	if err != nil {
		return fail(err)
	}
	if o.After != "" {
		fmt.Fprintf(o.Log, "%s: %s\n", site.ID, o.After)
		if err := after(ctx, o, site); err != nil {
			r.AfterError = err.Error()
		}
	}
	fmt.Fprintf(o.Log, "%s: compare with %q\n", site.ID, r.Snapshot)
	dir, err := st.NewDir("runs", store.Stamp(o.Now()))
	if err != nil {
		return fail(err)
	}
	run, err := runner.Run(ctx, runOpts)
	if run == nil {
		_ = os.Remove(dir)
		return fail(err)
	}
	if err := store.SaveRun(dir, run); err != nil {
		return fail(err)
	}
	// With the site's checks: levels and each action's ignore:, as
	// rhythm echo compare applies them.
	c := runner.CompareOptions(cfg).Compare(before, run, snapDir, dir, o.Version)
	c.Warnings = append(c.Warnings, run.Warnings...)
	if err := store.SaveComparison(dir, c); err != nil {
		return fail(err)
	}
	r.Dir, r.Verdict, r.Counts = dir, verdict(c), map[string]int{}
	for _, class := range model.Classes {
		r.Counts[class] = c.Count(class)
	}
	r.Finished = o.Now().UTC()
	if b, err := json.MarshalIndent(r, "", "  "); err == nil {
		_ = cache.WriteFile(filepath.Join(dir, "schedule.json"), b)
	}
	_ = st.Prune(keepWindows)
	return r
}

func snapshot(ctx context.Context, st store.Store, o runner.Options) (string, error) {
	dir, err := st.NewDir(store.Incoming, preSnapshot)
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(dir) }() //nolint:gosec // our own incoming folder
	o.Name = preSnapshot
	run, err := runner.Run(ctx, o)
	if err != nil {
		return "", err
	}
	if err := store.SaveRun(dir, run); err != nil {
		return "", err
	}
	return st.Promote(dir, preSnapshot)
}

// after runs the update command in the site's folder. Its output goes to
// the log; RHYTHM_ECHO_SITE and RHYTHM_ECHO_URL say which site and hub.
// The hub's tokens are hidden from it, so a plugin update in one client's
// site can't ask the hub for every client's mail. That is all it hides:
// the command runs as the hub's user and can still read the store's
// inbox folder, which holds every site's mail. Run it as another user
// (sudo -u www-data …) to keep it out of the store.
func after(ctx context.Context, o WindowOptions, site *Site) error {
	ctx, cancel := context.WithTimeout(ctx, o.AfterTimeout)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", o.After) //nolint:gosec // the operator's own command
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", o.After) //nolint:gosec // the operator's own command
	}
	cmd.Dir = site.ProjectDir()
	cmd.Env = append(childEnv(os.Environ(), o), "RHYTHM_ECHO_SITE="+site.ID, "RHYTHM_ECHO_URL="+o.URL)
	w := &prefixWriter{w: o.Log, prefix: site.ID + ": "}
	cmd.Stdout, cmd.Stderr = w, w
	err := cmd.Run()
	w.flush()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("still running after %s", o.AfterTimeout)
	}
	return err
}

// childEnv is env without the variables o.Hide names and without any
// whose value is a token the hub holds, in case one is set twice.
func childEnv(env []string, o WindowOptions) []string {
	secret := map[string]bool{}
	if o.Token != "" {
		secret[o.Token] = true
	}
	for _, s := range o.Sites {
		if s.token != "" {
			secret[s.token] = true
		}
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, value, _ := strings.Cut(kv, "=")
		if secret[value] || slices.ContainsFunc(o.Hide, func(h string) bool { return sameEnvName(h, name) }) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// sameEnvName compares variable names as the OS does: Windows ignores case.
func sameEnvName(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func verdict(c *model.Comparison) string {
	n := func(class string) int { return c.Count(class) }
	counts := fmt.Sprintf("%d broke, %d new, %d changed, %d fixed", n(model.Broke), n(model.New), n(model.Changed), n(model.Fixed))
	switch {
	case n(model.Broke) > 0:
		return "Something broke: " + counts + "."
	case n(model.New) > 0 || n(model.Changed) > 0:
		return "Something changed: " + counts + "."
	}
	return "Nothing broke: " + counts + "."
}

// History returns a site's window results since t, oldest first. It is
// what an agency report summarizes.
func History(hubStore, site string, since time.Time) ([]Result, error) {
	runs := filepath.Join(SiteStore(hubStore, site).Root, "runs")
	entries, err := os.ReadDir(runs)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Result
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(runs, e.Name(), "schedule.json")) //nolint:gosec // the hub's own store
		if err != nil {
			continue // a run made some other way
		}
		var r Result
		if json.Unmarshal(b, &r) == nil && !r.Started.Before(since) {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b Result) int { return a.Started.Compare(b.Started) })
	return out, nil
}

// prefixWriter writes each line with prefix, so the output of several
// sites' commands stays readable in one log.
type prefixWriter struct {
	w      io.Writer
	prefix string
	buf    []byte
}

func (p *prefixWriter) Write(b []byte) (int, error) {
	p.buf = append(p.buf, b...)
	for {
		i := slices.Index(p.buf, '\n')
		if i < 0 {
			break
		}
		fmt.Fprintf(p.w, "%s  %s\n", p.prefix, p.buf[:i])
		p.buf = p.buf[i+1:]
	}
	return len(b), nil
}

func (p *prefixWriter) flush() {
	if len(p.buf) > 0 {
		fmt.Fprintf(p.w, "%s  %s\n", p.prefix, p.buf)
		p.buf = nil
	}
}
