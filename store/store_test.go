// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package store

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/model"
)

func TestSnapshotLifecycle(t *testing.T) {
	s := Store{Root: filepath.Join(t.TempDir(), ".echo")}
	if _, err := s.Snapshot(""); err == nil || !strings.Contains(err.Error(), "no snapshot yet") {
		t.Fatalf("empty store: %v", err)
	}
	for _, name := range []string{"good", "after-update"} {
		dir, err := s.NewDir(Incoming, name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("kept"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := SaveRun(dir, &model.Run{Schema: model.Schema, Name: name, Target: "http://x.test/"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Promote(dir, name); err != nil {
			t.Fatal(err)
		}
		if err := s.SetLatest(name); err != nil {
			t.Fatal(err)
		}
	}
	dir, err := s.Snapshot("")
	if err != nil || filepath.Base(dir) != "after-update" {
		t.Fatalf("latest = %s, %v", dir, err)
	}
	r, err := LoadRun(dir)
	if err != nil || r.Name != "after-update" {
		t.Fatalf("LoadRun: %+v %v", r, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "notes.txt")); err != nil {
		t.Error("files didn't move with the snapshot")
	}
	names, _ := s.Snapshots()
	if strings.Join(names, ",") != "after-update,good" {
		t.Errorf("snapshots = %v", names)
	}
	if _, err := s.Snapshot("missing"); err == nil {
		t.Error("a missing snapshot must be an error")
	}
	if _, err := s.Snapshot("../etc"); err == nil {
		t.Error("path names must be rejected")
	}
}

func TestLoadRunRejectsOtherSchemas(t *testing.T) {
	dir := t.TempDir()
	_ = SaveRun(dir, &model.Run{Schema: "rhythm-echo/v0"})
	if _, err := LoadRun(dir); err == nil || !strings.Contains(err.Error(), "incompatible") {
		t.Errorf("got %v", err)
	}
}

func TestPruneKeepsNewest(t *testing.T) {
	s := Store{Root: t.TempDir()}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 5 {
		saveRun(t, s, start.Add(time.Duration(i)*time.Minute))
	}
	if err := s.Prune(2); err != nil {
		t.Fatal(err)
	}
	runs, _ := s.list(Runs)
	if len(runs) != 2 || !strings.HasPrefix(runs[1], Stamp(start.Add(4*time.Minute))+"-") {
		t.Errorf("kept %v", runs)
	}
	latest, _ := s.LatestRun()
	if filepath.Base(latest) != runs[1] {
		t.Errorf("LatestRun = %s", latest)
	}
}

// A run folder another process is still writing (no run.json yet) must
// survive a prune, however small keep is; one left behind by a crash
// goes once it is old.
func TestPruneLeavesUnfinishedRuns(t *testing.T) {
	s := Store{Root: t.TempDir()}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	writing, err := s.NewDir(Runs, Stamp(start))
	if err != nil {
		t.Fatal(err)
	}
	crashed, err := s.NewDir(Runs, Stamp(start.Add(time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * pruneGrace)
	if err := os.Chtimes(crashed, old, old); err != nil {
		t.Fatal(err)
	}
	finished := saveRun(t, s, start.Add(2*time.Minute))
	saveRun(t, s, start.Add(3*time.Minute))
	if err := s.Prune(1); err != nil {
		t.Fatal(err)
	}
	for dir, want := range map[string]bool{writing: true, crashed: false, finished: false} {
		if _, err := os.Stat(dir); (err == nil) != want {
			t.Errorf("%s kept: %v, want %v", filepath.Base(dir), err == nil, want)
		}
	}
}

func TestExcludeFromGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	t.Chdir(repo)
	s := Store{Root: ".echo"}
	s.ExcludeFromGit()
	s.ExcludeFromGit() // idempotent
	data, err := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	ex := string(data)
	if strings.Count(ex, "/.echo/snapshots/\n") != 1 || !strings.Contains(ex, "/.echo/runs/") {
		t.Errorf("exclude file:\n%s", ex)
	}
	if strings.Contains(ex, "/.echo/\n") {
		t.Error("config.yaml must stay visible to git")
	}
	want := "# Echo snapshots and reports\n/.echo/snapshots/\n/.echo/runs/\n/.echo/snapshots/.incoming-*\n"
	if !strings.HasSuffix(ex, want) {
		t.Errorf("exclude file:\n%s\nwant it to end in:\n%s", ex, want)
	}
	if strings.Contains(ex, "/.echo/.incoming/") {
		t.Error("the old .incoming/ folder is gone; its line must not be written")
	}
	// Each line must match what it's meant for and nothing else.
	for path, ignored := range map[string]bool{
		".echo/snapshots/before/run.json":                true,
		".echo/snapshots/.incoming-before/run.json":      true,
		".echo/runs/2026-01-01T00-00-00-abc123/run.json": true,
		".echo/config.yaml":                              false,
		".echo/flows/signup.yaml":                        false,
	} {
		err := exec.Command("git", "check-ignore", "-q", "--no-index", path).Run()
		if (err == nil) != ignored {
			t.Errorf("git check-ignore %s: ignored = %v, want %v", path, err == nil, ignored)
		}
	}
}

func TestExcludeFromGitAddsOnlyMissingLines(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	t.Chdir(repo)
	exclude := filepath.Join(repo, ".git", "info", "exclude")
	old := "# Echo snapshots and reports\n/.echo/snapshots/\n/.echo/runs/\n/.echo/.incoming/" // no final newline
	if err := os.WriteFile(exclude, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	Store{Root: ".echo"}.ExcludeFromGit()
	data, _ := os.ReadFile(exclude)
	if want := old + "\n/.echo/snapshots/.incoming-*\n"; string(data) != want {
		t.Errorf("exclude file:\n%q\nwant:\n%q", data, want)
	}
}

func TestExcludeFromGitSkipsIgnoredFolders(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	_ = os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("/work/\n"), 0o600)
	_ = os.MkdirAll(filepath.Join(repo, "work", "s01"), 0o750)
	t.Chdir(filepath.Join(repo, "work", "s01"))
	Store{Root: ".echo"}.ExcludeFromGit()
	data, _ := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if strings.Contains(string(data), "echo") {
		t.Errorf("an ignored folder mustn't add exclude lines:\n%s", data)
	}
}

func take(t *testing.T, s Store, name string, at time.Time) {
	t.Helper()
	dir, err := s.NewDir(Incoming, name)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveRun(dir, &model.Run{Schema: model.Schema, Name: name, Started: at}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Promote(dir, name); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLatest(name); err != nil {
		t.Fatal(err)
	}
}

func TestDeletedLatestFallsBackToNewest(t *testing.T) {
	s := Store{Root: filepath.Join(t.TempDir(), ".echo")}
	start := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	take(t, s, "zebra", start)                        // oldest, though it sorts last
	take(t, s, "before-update", start.Add(time.Hour)) // newest of the two that stay
	take(t, s, "newest", start.Add(2*time.Hour))

	// Removed behind the store's back (as `snapshots delete` did): the
	// stale pointer falls back to the newest remaining snapshot.
	if err := os.RemoveAll(filepath.Join(s.Root, "snapshots", "newest")); err != nil {
		t.Fatal(err)
	}
	if dir, err := s.Snapshot(""); err != nil || filepath.Base(dir) != "before-update" {
		t.Fatalf("after deleting the latest: %s, %v", dir, err)
	}
	if b, _ := os.ReadFile(filepath.Join(s.Root, "snapshots", ".latest")); strings.TrimSpace(string(b)) != "before-update" {
		t.Errorf(".latest = %q", b)
	}

	if err := s.Delete("before-update"); err != nil {
		t.Fatal(err)
	}
	if dir, err := s.Snapshot(""); err != nil || filepath.Base(dir) != "zebra" {
		t.Fatalf("after Delete: %s, %v", dir, err)
	}
	if err := s.Delete("zebra"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Snapshot(""); err == nil || !strings.Contains(err.Error(), "no snapshot yet") {
		t.Errorf("no snapshots left: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Root, "snapshots", ".latest")); !os.IsNotExist(err) {
		t.Errorf(".latest outlived the last snapshot: %v", err)
	}
	if err := s.Delete("nope"); err == nil {
		t.Error("deleting a missing snapshot must fail")
	}
}

func TestFailedSnapshotLeavesNothing(t *testing.T) {
	s := Store{Root: filepath.Join(t.TempDir(), ".echo")}
	if err := os.MkdirAll(filepath.Join(s.Root, Incoming), 0o750); err != nil { // left by an older version
		t.Fatal(err)
	}
	dir, err := s.NewDir(Incoming, "broken")
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveRun(dir, &model.Run{Schema: model.Schema}); err != nil {
		t.Fatal(err)
	}
	if names, _ := s.Snapshots(); len(names) != 0 {
		t.Errorf("an unfinished snapshot is listed: %v", names)
	}
	_ = os.RemoveAll(dir) // what the command does when the snapshot fails
	var left []string
	_ = filepath.WalkDir(s.Root, func(p string, d os.DirEntry, _ error) error {
		if p != s.Root && filepath.Base(p) != "snapshots" {
			left = append(left, p)
		}
		return nil
	})
	if len(left) != 0 {
		t.Errorf("left behind: %v", left)
	}
}

func TestLatestRunFallsBackToTheSnapshot(t *testing.T) {
	s := Store{Root: filepath.Join(t.TempDir(), ".echo")}
	if _, err := s.LatestRun(); err == nil || !strings.Contains(err.Error(), "nothing to show yet") {
		t.Fatalf("empty store: %v", err)
	}
	take(t, s, "first", time.Now())
	if dir, err := s.LatestRun(); err != nil || filepath.Base(dir) != "first" {
		t.Errorf("only a snapshot: %s, %v", dir, err)
	}
	run := saveRun(t, s, time.Now())
	if dir, err := s.LatestRun(); err != nil || dir != run {
		t.Errorf("a run since: %s, %v", dir, err)
	}
}

func saveRun(t *testing.T, s Store, at time.Time) string {
	t.Helper()
	dir, err := s.NewDir(Runs, Stamp(at))
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveRun(dir, &model.Run{Schema: model.Schema, Started: at}); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestLatestRunIsTheNewestResult: report with no argument shows whatever
// ran last, a compare or a snapshot.
func TestLatestRunIsTheNewestResult(t *testing.T) {
	start := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

	s := Store{Root: filepath.Join(t.TempDir(), ".echo")}
	take(t, s, "base", start)
	saveRun(t, s, start.Add(time.Minute)) // an old compare
	take(t, s, "fresh", start.Add(time.Hour))
	if dir, err := s.LatestRun(); err != nil || filepath.Base(dir) != "fresh" {
		t.Errorf("compare then snapshot: %s, %v", dir, err)
	}

	s = Store{Root: filepath.Join(t.TempDir(), ".echo")}
	take(t, s, "base", start)
	run := saveRun(t, s, start.Add(time.Hour))
	if dir, err := s.LatestRun(); err != nil || dir != run {
		t.Errorf("snapshot then compare: %s, %v", dir, err)
	}

	// A run that failed before writing run.json has nothing to show.
	if _, err := s.NewDir(Runs, Stamp(start.Add(2*time.Hour))); err != nil {
		t.Fatal(err)
	}
	if dir, err := s.LatestRun(); err != nil || dir != run {
		t.Errorf("an empty run folder won: %s, %v", dir, err)
	}

	// Without a started time the folder's age decides.
	stale := saveRun(t, s, time.Time{})
	old := start.Add(-time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if dir, _ := s.LatestRun(); dir != run {
		t.Errorf("an old folder without a time won: %s", dir)
	}
	if err := os.Chtimes(stale, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if dir, _ := s.LatestRun(); dir != stale {
		t.Errorf("a new folder without a time lost: %s", dir)
	}
}

// TestRunFoldersAreUnique: runs started in the same second, or by two
// processes at once, each get a folder; none is deleted.
func TestRunFoldersAreUnique(t *testing.T) {
	s := Store{Root: t.TempDir()}
	stamp := Stamp(time.Now())
	old := filepath.Join(s.Root, Runs, stamp)
	if err := os.MkdirAll(old, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "run.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	dirs := make([]string, 20)
	var wg sync.WaitGroup
	for i := range dirs {
		wg.Go(func() {
			dir, err := s.NewDir(Runs, stamp)
			if err != nil {
				t.Error(err)
				return
			}
			if err := os.WriteFile(filepath.Join(dir, "run.json"), []byte(dir), 0o600); err != nil {
				t.Error(err)
			}
			dirs[i] = dir
		})
	}
	wg.Wait()
	seen := map[string]bool{}
	for _, d := range dirs {
		if seen[d] || !regexp.MustCompile(`^`+regexp.QuoteMeta(stamp)+`-[0-9a-f]{6}$`).MatchString(filepath.Base(d)) {
			t.Errorf("folder %s is shared or misnamed", d)
		}
		seen[d] = true
		if b, err := os.ReadFile(filepath.Join(d, "run.json")); err != nil || string(b) != d {
			t.Errorf("%s was overwritten: %q %v", d, b, err)
		}
	}
	if b, err := os.ReadFile(filepath.Join(old, "run.json")); err != nil || string(b) != "{}" {
		t.Errorf("an existing folder was touched: %q %v", b, err)
	}
	if runs, _ := s.list(Runs); len(runs) != 21 {
		t.Errorf("%d run folders, want 21", len(runs))
	}
}

func TestOtherKindsNeverReplace(t *testing.T) {
	s := Store{Root: t.TempDir()}
	if _, err := s.NewDir("snapshots", "keep"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.NewDir("snapshots", "keep"); err == nil {
		t.Error("an existing folder must not be replaced")
	}
	if _, err := s.NewDir(Runs, "../x"); err == nil {
		t.Error("path names must be rejected")
	}
}

func TestReservedNames(t *testing.T) {
	s := Store{Root: t.TempDir()}
	for name, want := range map[string]string{
		"latest":     "reserved for the latest snapshot",
		"LATEST":     "reserved for the latest snapshot",
		"Latest":     "reserved for the latest snapshot",
		"CON":        "device name on Windows",
		"nul":        "device name on Windows",
		"Aux.tar.gz": "device name on Windows",
		"prn.txt":    "device name on Windows",
		"com1":       "device name on Windows",
		"LPT9.log":   "device name on Windows",
		"before.":    "ends in a dot",
		"a..":        "ends in a dot",
		"before ":    "isn't a valid name",
		"a b":        "isn't a valid name",
		".hidden":    "isn't a valid name",
		"":           "isn't a valid name",
		"../etc":     "isn't a valid name",
	} {
		err := CheckName(name)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("CheckName(%q) = %v, want %q", name, err, want)
		}
		if ValidName(name) {
			t.Errorf("ValidName(%q) = true", name)
		}
		if _, err := s.NewDir(Incoming, name); err == nil {
			t.Errorf("NewDir took %q", name)
		}
		if _, err := s.Promote(t.TempDir(), name); err == nil {
			t.Errorf("Promote took %q", name)
		}
	}
	for _, name := range []string{"before-update", "latest-2", "my.latest", "console", "com10", "lpt0", "auxiliary", "v1.2", Stamp(time.Now())} {
		if err := CheckName(name); err != nil {
			t.Errorf("CheckName(%q) = %v", name, err)
		}
	}
}

// TestCaseOnlyVariantsAreOneSnapshot: on macOS and Windows Before and
// before are one folder, so the store treats them as one everywhere.
func TestCaseOnlyVariantsAreOneSnapshot(t *testing.T) {
	s := Store{Root: filepath.Join(t.TempDir(), ".echo")}
	start := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	take(t, s, "Before", start)
	take(t, s, "before", start.Add(time.Hour))
	names, _ := s.Snapshots()
	if strings.Join(names, ",") != "before" {
		t.Fatalf("snapshots = %v, want one, spelled as the newest", names)
	}
	for _, n := range []string{"BEFORE", "Before", "before", ""} {
		dir, err := s.Snapshot(n)
		if err != nil || filepath.Base(dir) != "before" {
			t.Errorf("Snapshot(%q) = %s, %v", n, dir, err)
		}
	}
	if r, _ := LoadRun(filepath.Join(s.Root, "snapshots", "before")); !r.Started.Equal(start.Add(time.Hour)) {
		t.Errorf("kept the older snapshot: %v", r.Started)
	}
	if err := s.Delete("BEFORE"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Snapshot(""); err == nil || !strings.Contains(err.Error(), "no snapshot yet") {
		t.Errorf("after deleting the only one: %v", err)
	}
}

// TestPointerSurvivesASnapshotNamedLATEST: older versions let a snapshot
// take the pointer's name. Such a folder stays readable and every later
// command leaves no stray file.
func TestPointerSurvivesASnapshotNamedLATEST(t *testing.T) {
	s := Store{Root: filepath.Join(t.TempDir(), ".echo")}
	take(t, s, "good", time.Now())
	legacy := filepath.Join(s.Root, "snapshots", "LATEST")
	if err := os.MkdirAll(legacy, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := SaveRun(legacy, &model.Run{Schema: model.Schema, Name: "LATEST", Started: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if dir, err := s.Snapshot(""); err != nil || filepath.Base(dir) != "good" {
			t.Fatalf("latest = %s, %v", dir, err)
		}
	}
	if dir, err := s.Snapshot("LATEST"); err != nil || dir != legacy {
		t.Errorf("the legacy snapshot is unreachable: %s, %v", dir, err)
	}
	if left := strays(t, s); len(left) != 0 {
		t.Errorf("left behind: %v", left)
	}
}

func TestOldLatestFileMigrates(t *testing.T) {
	s := Store{Root: filepath.Join(t.TempDir(), ".echo")}
	start := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	take(t, s, "older", start)
	take(t, s, "newer", start.Add(time.Hour))
	snaps := filepath.Join(s.Root, "snapshots")
	_ = os.Remove(filepath.Join(snaps, ".latest"))
	// What an older version left: its pointer naming the older snapshot
	// and the temp files of pointer writes that failed.
	if err := os.WriteFile(filepath.Join(snaps, "LATEST"), []byte("older\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snaps, ".tmp-3365011007"), []byte("LATEST\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if dir, err := s.Snapshot(""); err != nil || filepath.Base(dir) != "older" {
		t.Fatalf("the old pointer was lost: %s, %v", dir, err)
	}
	if b, _ := os.ReadFile(filepath.Join(snaps, ".latest")); string(b) != "older\n" {
		t.Errorf(".latest = %q", b)
	}
	if left := strays(t, s); len(left) != 0 {
		t.Errorf("left behind: %v", left)
	}
	// Once moved, the new pointer wins over a LATEST written again.
	_ = os.WriteFile(filepath.Join(snaps, "LATEST"), []byte("newer\n"), 0o600)
	if dir, _ := s.Snapshot(""); filepath.Base(dir) != "older" {
		t.Errorf("latest = %s", dir)
	}
	if _, err := os.Stat(filepath.Join(snaps, "LATEST")); !os.IsNotExist(err) {
		t.Errorf("LATEST is still there: %v", err)
	}
}

func TestFailedPointerWriteLeavesNoTempFile(t *testing.T) {
	s := Store{Root: filepath.Join(t.TempDir(), ".echo")}
	take(t, s, "good", time.Now())
	snaps := filepath.Join(s.Root, "snapshots")

	// The rename fails: a folder holds the pointer's place.
	_ = os.Remove(filepath.Join(snaps, ".latest"))
	if err := os.MkdirAll(filepath.Join(snaps, ".latest", "x"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLatest("good"); err == nil {
		t.Error("SetLatest over a folder must fail")
	}
	if left := strays(t, s); len(left) != 0 {
		t.Errorf("a failed rename left: %v", left)
	}
	_ = os.RemoveAll(filepath.Join(snaps, ".latest"))

	// A read-only folder.
	if os.Geteuid() == 0 {
		t.Skip("root ignores folder permissions")
	}
	if err := os.Chmod(snaps, 0o500); err != nil { //nolint:gosec // a test folder made read-only on purpose
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(snaps, 0o750) }) //nolint:gosec // restore the test folder
	if err := s.SetLatest("good"); err == nil {
		t.Error("SetLatest in a read-only folder must fail")
	}
	if left := strays(t, s); len(left) != 0 {
		t.Errorf("a read-only folder left: %v", left)
	}
}

// strays lists the entries of snapshots/ that are neither a snapshot nor
// the pointer.
func strays(t *testing.T, s Store) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(s.Root, "snapshots"))
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, e := range entries {
		if !e.IsDir() && e.Name() != ".latest" {
			left = append(left, e.Name())
		}
	}
	return left
}

// TestFoldersAreOwnerOnly: every folder the store makes is 0700, whoever
// calls it (the CLI, MCP or the hub). A runs or snapshots folder an
// older version made 0750 is tightened.
func TestFoldersAreOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no Unix modes")
	}
	s := Store{Root: filepath.Join(t.TempDir(), ".echo")}
	if err := os.MkdirAll(filepath.Join(s.Root, Runs), 0o750); err != nil { //nolint:gosec // an older version's mode, on purpose
		t.Fatal(err)
	}
	run, err := s.NewDir(Runs, "r")
	if err != nil {
		t.Fatal(err)
	}
	in, err := s.NewDir(Incoming, "good")
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveRun(in, &model.Run{Schema: model.Schema, Name: "good"}); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Promote(in, "good")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{filepath.Join(s.Root, Runs), run, filepath.Join(s.Root, "snapshots"), snap} {
		fi, err := os.Stat(d)
		if err != nil {
			t.Fatal(err)
		}
		if m := fi.Mode().Perm(); m != 0o700 {
			t.Errorf("%s is %o, want 700", d, m)
		}
	}
}
