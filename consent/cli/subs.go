// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"syscall"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/evidence"
	"github.com/RhythmShahriar/echo/store"
)

// detectDir is the folder under the store's consent/ where consent
// detect keeps its screenshots; prune never touches it.
const detectDir = "detect"

// detectStages are what consent detect needs: the browser, the CMP
// rules and the catalog that holds the hits.
var detectStages = []consent.Stage{consent.StageClassifier, consent.StageCMP, consent.StageRunner}

// detect is rhythm echo consent detect <url>: it loads the page once
// (hits held), waits for the banner and finds both buttons without
// clicking. Exit 0 a banner and both buttons, 1 a banner with a button
// missing, 3 no banner.
func (c *cmd) detect(pos []string) int {
	env := c.env
	if len(pos) != 1 {
		return fail(env, usagef("consent detect needs one page: rhythm echo consent detect https://staging.shop.test"))
	}
	st, err := c.prepare(pos)
	if err != nil {
		return fail(env, err)
	}
	for _, n := range st.notes {
		fmt.Fprintln(env.Stderr, "rhythm echo:", n)
	}
	if err := consent.Need(detectStages...); err != nil {
		return fail(env, err)
	}
	for _, w := range st.warn {
		fmt.Fprintln(env.Stderr, "rhythm echo:", w)
	}
	p := st.plans[0]
	if p.Browser.NoSandbox || noSandboxEnv() {
		fmt.Fprintln(env.Stderr, "rhythm echo:", msgNoSandbox)
	}
	dir := filepath.Join(st.store.Root, store.Consent, detectDir)
	for _, d := range []string{st.store.Root, filepath.Join(st.store.Root, store.Consent), dir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fail(env, &writeError{d, err})
		}
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	st.store.ExcludeFromGit()
	p.Dir, p.ID = dir, store.Stamp(env.now())+"-"+randomHex()
	p.Browser.Log = env.Stderr

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	d, err := consent.Linked().Runner.Detect(ctx, p)
	if ctx.Err() != nil {
		fmt.Fprintln(env.Stderr, "rhythm echo: interrupted")
		return exitInterrupted
	}
	if err != nil {
		var fe *consent.FatalError
		if errors.As(err, &fe) {
			fmt.Fprintln(env.Stderr, "rhythm echo:", fatalMessage(fe))
			return exitUnavailable
		}
		return fail(env, err)
	}
	d.URL = consent.MaskURL(d.URL, false)
	if d.Screenshot != "" {
		shot := d.Screenshot
		if !filepath.IsAbs(shot) {
			shot = filepath.Join(dir, filepath.FromSlash(shot))
		}
		d.Screenshot = shown(shot)
	}
	opts := c.textOptions(env.Stdout)
	opts.BannerWait = p.BannerWait
	if err := writers.detect(env.Stdout, d, c.o.json, opts); err != nil {
		fmt.Fprintln(env.Stderr, "rhythm echo: printing the result:", err)
	}
	return detectExit(d)
}

// detectExit is consent detect's exit code: 3 no banner, 1 a banner
// with the Accept or Reject button missing, 0 both found.
func detectExit(d *consent.DetectResult) int {
	switch {
	case !d.CMP.Found:
		return exitUnavailable
	case d.Accept == nil || d.Reject == nil:
		return exitFindings
	}
	return exitOK
}

// randomHex is 6 random hex digits, as a result folder's name ends.
func randomHex() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// latestWord is consent report's default: the newest result.
const latestWord = "latest"

// report is rhythm echo consent report [<id>|latest]: it shows a saved
// result again in the formats asked for, compares it with --against
// and exits as the check would have for it. The evidence folder isn't
// written to: its files are hashed, so a re-render goes to stdout and
// -o files only.
func (c *cmd) report(pos []string) int {
	env, o := c.env, c.o
	if len(pos) > 1 {
		return fail(env, usagef("consent report takes one result: an id from .echo/consent, a folder or latest"))
	}
	outs, err := o.reports()
	if err != nil {
		return fail(env, err)
	}
	st := &setup{outs: outs}
	if st.failOn, err = checkFailOn(o.failOn, o.against != ""); err != nil {
		return fail(env, err)
	}
	if err := st.loadConfig(o.configPath); err != nil {
		return fail(env, err)
	}
	st.store = store.Store{Root: storeRoot(o.storeDir, st.cfgPath)}
	for _, n := range st.notes {
		fmt.Fprintln(env.Stderr, "rhythm echo:", n)
	}
	which := latestWord
	if len(pos) == 1 {
		which = pos[0]
	}
	dir, err := resultFolder(st.store, which)
	if err != nil {
		return fail(env, err)
	}
	path := filepath.Join(dir, evidence.ResultFile)
	r, err := readResult(which, path)
	if err != nil {
		// readResult words its errors for --against.
		return fail(env, usagef("consent report %s", trimAgainst(err.Error(), which)))
	}
	// consent.json says whether the check was complete; the interrupted
	// note and an inconclusive exit say it too, so a file edited by hand
	// can't claim more than its runs show.
	interrupted := slices.Contains(r.Notes, noteInterrupted)
	r.Complete = r.Complete && !interrupted && r.ExitCode(failOnNone) != exitUnavailable
	if o.against != "" {
		if err := consent.Need(consent.StageComparer); err != nil {
			return fail(env, err)
		}
		base, err := loadAgainst(st.store, o.against)
		if err != nil {
			return fail(env, err)
		}
		if r.Comparison = consent.Compare(base, r, o.accept); r.Comparison != nil {
			r.Comparison.Against = o.against
		}
	}
	sha := ""
	if f, err := evidence.HashFile(path); err == nil {
		sha = f.SHA256
	}
	write := c.formatWriter(r, dir, sha, st.failOn)
	for _, out := range st.outs {
		if out.path == "" {
			if err := write(env.Stdout, out.format); err != nil {
				return fail(env, fmt.Errorf("writing the report: %w", err))
			}
			continue
		}
		if err := writeFile(out.path, func(w io.Writer) error { return write(w, out.format) }); err != nil {
			return fail(env, err)
		}
	}
	view := env.Stdout
	if stdoutFormat(st.outs) != "" {
		view = env.Stderr
	}
	opts := c.textOptions(view)
	html := filepath.Join(dir, reportHTML)
	opts.FailOn, opts.ReportPath = st.failOn, shown(html)
	if _, err := os.Stat(html); err != nil {
		opts.ReportPath = ""
	}
	if err := writers.text(view, r, opts); err != nil {
		fmt.Fprintln(env.Stderr, "rhythm echo: printing the result:", err)
	}
	if o.open && env.Open != nil && opts.ReportPath != "" {
		if err := env.Open(html); err != nil {
			fmt.Fprintln(env.Stderr, "rhythm echo: opening the report:", err)
		}
	}
	if interrupted {
		// The check itself exited 130 for this result: so does report.
		return exitInterrupted
	}
	return r.ExitCode(st.failOn)
}

// trimAgainst rewords readResult's "--against <what>: ..." for report.
func trimAgainst(msg, what string) string {
	prefix := "--against " + what + ": "
	if len(msg) > len(prefix) && msg[:len(prefix)] == prefix {
		return what + ": " + msg[len(prefix):]
	}
	return msg
}

// resultFolder finds the result consent report names: latest (the
// newest), a folder or consent.json that exists, or a result's id in
// the store.
func resultFolder(st store.Store, which string) (string, error) {
	root := shown(filepath.Join(st.Root, store.Consent))
	if which == latestWord {
		dir, err := latestResult(st)
		if err != nil {
			return "", err
		}
		if dir == "" {
			return "", usagef("consent report: no consent result in %s yet; run rhythm echo consent <url> first", root)
		}
		return dir, nil
	}
	if fi, err := os.Stat(which); err == nil {
		dir := which
		if !fi.IsDir() {
			dir = filepath.Dir(which)
		}
		if _, err := os.Stat(filepath.Join(dir, evidence.ResultFile)); err != nil {
			return "", usagef("consent report %s: no %s in that folder", which, evidence.ResultFile)
		}
		return dir, nil
	}
	if store.CheckName(which) == nil && which != "baselines" && which != detectDir {
		dir := filepath.Join(st.Root, store.Consent, which)
		if _, err := os.Stat(filepath.Join(dir, evidence.ResultFile)); err == nil {
			return dir, nil
		}
	}
	return "", usagef("consent report %q: no consent result by that id in %s; rhythm echo consent report latest shows the newest", which, root)
}

// cmps is rhythm echo consent cmps: the CMP rules, as a table or JSON.
func (c *cmd) cmps(pos []string) int {
	if len(pos) > 0 {
		return fail(c.env, usagef("consent cmps takes no arguments"))
	}
	if err := consent.Need(consent.StageCMP); err != nil {
		return fail(c.env, err)
	}
	if err := writers.catalog(c.env.Stdout, consent.Linked().CMP.Rules(), nil, string(subCmps), c.o.json, c.textOptions(c.env.Stdout)); err != nil {
		return fail(c.env, fmt.Errorf("printing the list: %w", err))
	}
	return exitOK
}

// trackers is rhythm echo consent trackers: the tracker catalog with
// the config's own trackers (consent: trackers:), as a table or JSON.
func (c *cmd) trackers(pos []string) int {
	env := c.env
	if len(pos) > 0 {
		return fail(env, usagef("consent trackers takes no arguments"))
	}
	if err := consent.Need(consent.StageClassifier); err != nil {
		return fail(env, err)
	}
	st := &setup{}
	if err := st.loadConfig(c.o.configPath); err != nil {
		return fail(env, err)
	}
	for _, n := range st.notes {
		fmt.Fprintln(env.Stderr, "rhythm echo:", n)
	}
	if st.cfg.Consent != nil {
		if err := st.customTrackers(&consent.Plan{}, st.cfg.Consent); err != nil {
			return fail(env, err)
		}
	}
	if err := writers.catalog(env.Stdout, nil, consent.Linked().Classifier.Trackers(), string(subTrackers), c.o.json, c.textOptions(env.Stdout)); err != nil {
		return fail(env, fmt.Errorf("printing the list: %w", err))
	}
	return exitOK
}

// defaultKeep is how many results prune keeps without --keep.
const defaultKeep = 20

// resultName is a result folder's name: its start time and 6 hex
// digits. prune removes only folders named so.
var resultName = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}-[0-9a-f]{6}$`)

// prune is rhythm echo consent prune [--keep n]: it deletes the result
// folders beyond the newest n, never baselines/ or detect/.
func (c *cmd) prune(pos []string) int {
	env, o := c.env, c.o
	if len(pos) > 0 {
		return fail(env, usagef("consent prune takes no arguments; --keep <n> says how many results stay"))
	}
	keep := defaultKeep
	if o.set["keep"] {
		if o.keep < 0 {
			return fail(env, usagef("--keep %d: want 0 or more", o.keep))
		}
		keep = o.keep
	}
	st := &setup{}
	if err := st.loadConfig(o.configPath); err != nil {
		return fail(env, err)
	}
	st.store = store.Store{Root: storeRoot(o.storeDir, st.cfgPath)}
	root := filepath.Join(st.store.Root, store.Consent)
	entries, err := os.ReadDir(root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fail(env, err)
	}
	var dirs []string
	for _, e := range entries {
		// ReadDir doesn't follow links: a link named like a result is
		// left alone, as is everything not named like one.
		if e.IsDir() && resultName.MatchString(e.Name()) {
			dirs = append(dirs, e.Name())
		}
	}
	slices.Sort(dirs)
	removed := 0
	if len(dirs) > keep {
		for _, name := range dirs[:len(dirs)-keep] {
			if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
				return fail(env, fmt.Errorf("removing %s: %w", shown(filepath.Join(root, name)), err))
			}
			removed++
		}
	}
	if !o.quiet {
		fmt.Fprintf(env.Stdout, "Removed %d consent results, kept %d.\n", removed, len(dirs)-removed)
	}
	return exitOK
}
