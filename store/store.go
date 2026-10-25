// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package store keeps Echo's files in the project's .echo/
// folder: named snapshots, recent runs and comparisons, each a directory
// holding run.json (or comparison.json).
package store

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/rhythm/kit/cache"
)

// Incoming holds a snapshot while it's being taken, so a failed or
// canceled snapshot never replaces a good one with the same name. Its
// folders sit next to the snapshots, hidden (snapshots/.incoming-<name>),
// so removing one leaves nothing behind.
const Incoming = ".incoming"

// Store is a .echo folder.
type Store struct{ Root string }

// safeName also rules out spaces, so a name can't end in one (Windows
// drops trailing spaces and dots from file names).
var safeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// windowsDevice matches the names Windows keeps for devices, which no
// file or folder can have there, with or without an extension (nul.txt
// is NUL too).
var windowsDevice = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[1-9]|lpt[1-9])(\.|$)`)

// Reserved is the snapshot name kept for the latest snapshot, in any case.
const Reserved = "latest"

// ValidName reports whether name can be used for a new snapshot or run.
func ValidName(name string) bool { return CheckName(name) == nil }

// CheckName says why name can't be used for a new snapshot or run, or
// returns nil. Names must work as folder names on Linux, macOS and
// Windows, so a project's .echo folder can move between them.
func CheckName(name string) error {
	switch {
	case !safeName.MatchString(name):
		return fmt.Errorf("%q isn't a valid name: use up to 64 letters, digits, dots, dashes and underscores, starting with a letter or digit", name)
	case strings.EqualFold(name, Reserved):
		return fmt.Errorf("%q is reserved for the latest snapshot; pick another name", name)
	case windowsDevice.MatchString(name):
		return fmt.Errorf("%q is a device name on Windows (CON, PRN, AUX, NUL, COM1 to COM9, LPT1 to LPT9); pick another name", name)
	case strings.HasSuffix(name, "."):
		return fmt.Errorf("%q ends in a dot, which Windows drops; pick another name", name)
	}
	return nil
}

// Stamp is the default name for snapshots and runs.
func Stamp(t time.Time) string { return t.Format("2006-01-02T15-04-05") }

// Runs holds recent runs and comparisons, one folder each.
const Runs = "runs"

// dirMode is the mode of every folder the store makes: its files hold
// captured messages (recipients, bodies), so only the owner may list
// them, as only the owner may read the files (0600).
const dirMode = 0o700

// mkdirParent makes the folder dir sits in (runs or snapshots) and makes
// it dirMode if an older version left it more open.
func mkdirParent(dir string) error {
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, dirMode); err != nil {
		return err
	}
	return os.Chmod(parent, dirMode)
}

// NewDir creates a folder for kind and returns it. For Runs it's
// runs/<name>-<6 random hex digits>, made with a plain mkdir that retries
// on a clash, so runs started in the same second (Stamp has one-second
// resolution) or by two processes at once each get their own folder and
// none is ever deleted. For Incoming it's the in-progress folder of the
// snapshot name, replaced when a failed snapshot left one behind. Any
// other kind gets kind/name, which must not exist yet.
func (s Store) NewDir(kind, name string) (string, error) {
	if err := CheckName(name); err != nil {
		return "", err
	}
	switch kind {
	case Incoming:
		_ = os.Remove(filepath.Join(s.Root, Incoming)) // an empty one left by older versions
		dir := filepath.Join(s.Root, "snapshots", Incoming+"-"+name)
		if err := os.RemoveAll(dir); err != nil {
			return "", err
		}
		if err := mkdirParent(dir); err != nil {
			return "", err
		}
		return dir, os.Mkdir(dir, dirMode)
	case Runs:
		parent := filepath.Join(s.Root, Runs)
		if err := mkdirParent(filepath.Join(parent, "x")); err != nil {
			return "", err
		}
		for range 16 {
			dir := filepath.Join(parent, name+"-"+randomHex(3))
			err := os.Mkdir(dir, dirMode)
			if err == nil {
				return dir, nil
			}
			if !errors.Is(err, fs.ErrExist) {
				return "", err
			}
		}
		return "", fmt.Errorf("no free folder for %s in %s", name, parent)
	}
	dir := filepath.Join(s.Root, kind, name)
	if err := mkdirParent(dir); err != nil {
		return "", err
	}
	return dir, os.Mkdir(dir, dirMode)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// SaveRun writes run.json into dir.
func SaveRun(dir string, r *model.Run) error {
	return cache.WriteJSON(filepath.Join(dir, "run.json"), r)
}

// LoadRun reads run.json from dir.
func LoadRun(dir string) (*model.Run, error) {
	var r model.Run
	if err := cache.ReadJSON(filepath.Join(dir, "run.json"), &r); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("no run in %s", dir)
		}
		return nil, fmt.Errorf("%s: %w", dir, err)
	}
	if r.Schema != model.Schema {
		return nil, fmt.Errorf("%s was made by an incompatible Echo (%s); take a new snapshot", dir, r.Schema)
	}
	return &r, nil
}

// SaveComparison writes comparison.json into dir.
func SaveComparison(dir string, c *model.Comparison) error {
	return cache.WriteJSON(filepath.Join(dir, "comparison.json"), c)
}

// LoadComparison reads comparison.json from dir.
func LoadComparison(dir string) (*model.Comparison, error) {
	var c model.Comparison
	if err := cache.ReadJSON(filepath.Join(dir, "comparison.json"), &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// Promote moves a finished snapshot from Incoming into snapshots/name,
// replacing an older snapshot with that name; it returns the new folder.
// Names that differ only in case are one snapshot: macOS and Windows
// can't keep Before and before apart, so taking before replaces Before
// everywhere and the folder takes the new spelling.
func (s Store) Promote(dir, name string) (string, error) {
	if err := CheckName(name); err != nil {
		return "", err
	}
	final := filepath.Join(s.Root, "snapshots", name)
	if err := os.MkdirAll(filepath.Dir(final), dirMode); err != nil {
		return "", err
	}
	names, err := s.Snapshots()
	if err != nil {
		return "", err
	}
	for _, n := range names {
		if strings.EqualFold(n, name) {
			if err := os.RemoveAll(filepath.Join(s.Root, "snapshots", n)); err != nil {
				return "", err
			}
		}
	}
	return final, os.Rename(dir, final)
}

// SetLatest records name as the snapshot compare uses by default.
func (s Store) SetLatest(name string) error {
	return writeAtomic(s.latestFile(), []byte(name+"\n"))
}

// latestFile is the pointer to the latest snapshot. It's a dotfile so no
// snapshot folder can take its place; older versions kept it in LATEST,
// which a snapshot named LATEST destroyed.
func (s Store) latestFile() string { return filepath.Join(s.Root, "snapshots", ".latest") }

// latest returns the name the pointer holds, or "".
func (s Store) latest() string {
	s.migrateLatest()
	b, _ := os.ReadFile(s.latestFile())
	return strings.TrimSpace(string(b))
}

// migrateLatest moves an older version's snapshots/LATEST file to
// .latest, once, then removes the temp files it leaked each time writing
// LATEST failed (kit/cache.WriteFile before rhythm v0.6.0 kept its temp
// when the rename failed). A LATEST folder is a snapshot and stays.
func (s Store) migrateLatest() {
	snaps := filepath.Join(s.Root, "snapshots")
	old := filepath.Join(snaps, "LATEST")
	if fi, err := os.Lstat(old); err == nil && fi.Mode().IsRegular() {
		if _, err := os.Lstat(s.latestFile()); errors.Is(err, fs.ErrNotExist) {
			b, err := os.ReadFile(old) //nolint:gosec // our own pointer file
			if err != nil || writeAtomic(s.latestFile(), b) != nil {
				return
			}
		}
		_ = os.Remove(old)
	}
	// Only the old pointer was ever written straight into snapshots/.
	leaked, _ := filepath.Glob(filepath.Join(snaps, ".tmp-*"))
	for _, p := range leaked {
		if fi, err := os.Lstat(p); err == nil && fi.Mode().IsRegular() {
			_ = os.Remove(p)
		}
	}
}

// writeAtomic writes data to path through a temp file in the same folder
// and a rename, removing the temp file whenever a step fails, so a
// failure never leaves a half-written pointer or a stray file behind.
// It stays apart from kit/cache.WriteFile, which now cleans up the same
// way, for its temp name: the kit's .tmp-* is what migrateLatest removes
// as an old leak, so another Echo's cleanup could delete it mid-write.
func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}

// Snapshot returns the directory of a named snapshot, or of the latest
// one when name is empty. When the latest was deleted, the newest
// remaining snapshot becomes the latest. Case doesn't matter, as on
// macOS and Windows.
func (s Store) Snapshot(name string) (string, error) {
	if name == "" {
		name = s.latest()
		if s.find(name) == "" {
			newest, err := s.newest()
			if err != nil {
				return "", err
			}
			if newest == "" {
				return "", errors.New("no snapshot yet; take one first: rhythm echo snapshot")
			}
			name = newest
			_ = s.SetLatest(name)
		}
	}
	// Lookups check only the characters, not CheckName, so a snapshot an
	// older version let through (LATEST, con) can still be read and deleted.
	if !safeName.MatchString(name) {
		return "", fmt.Errorf("%q isn't a valid snapshot name", name)
	}
	found := s.find(name)
	if found == "" {
		return "", fmt.Errorf("no snapshot named %q; see rhythm echo snapshots", name)
	}
	return filepath.Join(s.Root, "snapshots", found), nil
}

// Delete removes a snapshot. When it was the latest, the newest remaining
// one becomes the latest.
func (s Store) Delete(name string) error {
	dir, err := s.Snapshot(name)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if !strings.EqualFold(s.latest(), filepath.Base(dir)) {
		return nil
	}
	newest, err := s.newest()
	if err != nil {
		return err
	}
	if newest == "" {
		return os.Remove(s.latestFile())
	}
	return s.SetLatest(newest)
}

// find returns the folder name of the finished snapshot called name,
// ignoring case and preferring an exact match, or "" when there's none.
func (s Store) find(name string) string {
	if !safeName.MatchString(name) {
		return ""
	}
	has := func(n string) bool {
		_, err := os.Stat(filepath.Join(s.Root, "snapshots", n, "run.json")) //nolint:gosec // n passed safeName or came from ReadDir
		return err == nil
	}
	// The folder's own spelling wins: on macOS and Windows a Stat of
	// BEFORE finds the folder before, which must still be named before.
	names, _ := s.Snapshots()
	for _, n := range names {
		if n == name && has(n) {
			return n
		}
	}
	for _, n := range names {
		if strings.EqualFold(n, name) && has(n) {
			return n
		}
	}
	if has(name) {
		return name
	}
	return ""
}

// newest returns the most recently taken snapshot, or "" when there's
// none. Names don't sort by time ("before-update"), so it reads them.
func (s Store) newest() (string, error) {
	names, err := s.Snapshots()
	if err != nil {
		return "", err
	}
	best, bestAt := "", time.Time{}
	for _, n := range names {
		r, err := LoadRun(filepath.Join(s.Root, "snapshots", n))
		if err != nil {
			continue
		}
		if best == "" || !r.Started.Before(bestAt) {
			best, bestAt = n, r.Started
		}
	}
	return best, nil
}

// Snapshots lists snapshot names, sorted by name.
func (s Store) Snapshots() ([]string, error) {
	return s.list("snapshots")
}

// LatestRun returns the folder of the newest result, a run, comparison
// or snapshot, by when it started (run.json's started, or the folder's
// modification time when that can't be read). Folders without run.json,
// left by a run that failed early, have nothing to show and are skipped.
// On a tie a run wins, since it was made from the snapshot.
func (s Store) LatestRun() (string, error) {
	best, bestAt := "", time.Time{}
	for _, kind := range []string{"snapshots", Runs} { // runs last, so they win ties
		names, _ := s.list(kind)
		for _, n := range names {
			dir := filepath.Join(s.Root, kind, n)
			at, ok := startedAt(dir)
			if ok && (best == "" || !at.Before(bestAt)) {
				best, bestAt = dir, at
			}
		}
	}
	if best == "" {
		return "", errors.New("nothing to show yet; run rhythm echo snapshot, compare or run first")
	}
	return best, nil
}

// startedAt returns when the run in dir started, or false when dir holds
// no run.json. It reads only the started field.
func startedAt(dir string) (time.Time, bool) {
	path := filepath.Join(dir, "run.json")
	if _, err := os.Stat(path); err != nil {
		return time.Time{}, false
	}
	var r struct {
		Started time.Time `json:"started"`
	}
	if cache.ReadJSON(path, &r) == nil && !r.Started.IsZero() {
		return r.Started, true
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return time.Time{}, false
	}
	return fi.ModTime(), true
}

func (s Store) list(kind string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(s.Root, kind))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") { // .incoming-* are unfinished
			names = append(names, e.Name())
		}
	}
	slices.Sort(names) // stamps sort chronologically
	return names, err
}

// Prune keeps the newest keep runs and deletes older ones. Snapshots are
// never pruned. A folder without run.json or comparison.json is a run
// another process may still be writing, so it is left alone until it is
// pruneGrace old (by its modification time), which clears out runs that
// crashed without deleting one still going.
func (s Store) Prune(keep int) error {
	runs, err := s.list(Runs)
	if err != nil {
		return err
	}
	for _, n := range runs[:max(len(runs)-keep, 0)] {
		dir := filepath.Join(s.Root, Runs, n)
		if !finished(dir) && !older(dir, pruneGrace) {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
	}
	return nil
}

// pruneGrace is how long Prune leaves an unfinished run folder alone. A
// run takes a minute per action at most, so an hour is well past any
// that is still going.
const pruneGrace = time.Hour

// finished reports whether dir holds a written run or comparison.
func finished(dir string) bool {
	for _, f := range []string{"run.json", "comparison.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return true
		}
	}
	return false
}

// older reports whether dir was last modified more than d ago.
func older(dir string, d time.Duration) bool {
	fi, err := os.Stat(dir)
	return err == nil && time.Since(fi.ModTime()) > d
}

// ExcludeFromGit adds the store's snapshots and runs to the repository's
// local exclude list (.git/info/exclude), so captured messages, which
// can hold customer data, never get committed by accident and the
// project's .gitignore stays untouched. Unfinished snapshots
// (snapshots/.incoming-<name>) get a line of their own; older versions
// wrote a .incoming/ line for a folder Echo no longer uses. An
// exclude list they wrote gets only the lines it lacks. config.yaml and
// flows/ stay visible to git, to share with the team. It does nothing
// outside a git repository.
func (s Store) ExcludeFromGit() {
	abs, err := filepath.Abs(s.Root)
	if err != nil {
		return
	}
	// git runs in the store's parent folder and names paths from there,
	// so no path git prints is compared with ours: on macOS the temporary
	// folders sit behind a symlink (/var is /private/var) and on Windows
	// git spells C:\Users\RUNNER~1 as C:/Users/runneradmin.
	dir, name := filepath.Dir(abs), filepath.Base(abs)
	// git needs a folder that exists: start from the nearest one.
	for {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
		name = filepath.Base(dir) + "/" + name
		dir = parent
	}
	git := func(args ...string) (string, error) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output() //nolint:gosec // fixed git subcommands on our own store path
		return strings.TrimSpace(string(out)), err
	}
	path, err := git("rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	// The parent's path below the repository's top, "" at the top.
	prefix, err := git("rev-parse", "--show-prefix")
	if err != nil {
		return
	}
	// Already ignored (e.g. inside a build or work folder): nothing to add.
	if _, err := git("check-ignore", "-q", name); err == nil {
		return
	}
	base := "/" + prefix + name + "/"
	lines := []string{base + "snapshots/", base + "runs/", base + "snapshots/" + Incoming + "-*"}
	existing, _ := os.ReadFile(path) //nolint:gosec // the repository's own exclude file
	have := strings.Split(string(existing), "\n")
	var missing []string
	for _, l := range lines {
		if !slices.Contains(have, l) {
			missing = append(missing, l)
		}
	}
	if len(missing) == 0 {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec // the repository's own exclude file
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		_, _ = f.WriteString("\n")
	}
	header := ""
	if len(missing) == len(lines) {
		header = "# Echo snapshots and reports\n"
	}
	_, _ = f.WriteString(header + strings.Join(missing, "\n") + "\n")
}
