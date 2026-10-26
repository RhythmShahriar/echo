// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/evidence"
	"github.com/RhythmShahriar/echo/store"
	"github.com/RhythmShahriar/rhythm/kit/cache"
)

// maxResultFile bounds a consent.json read back: a real one is a few
// MiB at most.
const maxResultFile = 64 << 20

// lastWord is --against last: the previous result.
const lastWord = "last"

// baselinesDir is where --save keeps baselines in st.
func baselinesDir(st store.Store) string {
	return filepath.Join(st.Root, filepath.FromSlash(store.ConsentBaselines))
}

// loadAgainst reads what --against names, before anything runs: last
// (the newest result in the store), a consent.json, a result folder or
// a baseline's name. A missing one is a usage error (PRD 10.2 message
// 5 for a name).
func loadAgainst(st store.Store, against string) (*consent.Result, error) {
	if against == lastWord {
		dir, err := latestResult(st)
		if err != nil {
			return nil, err
		}
		if dir == "" {
			return nil, usagef("--against last: no consent result in %s yet; run rhythm echo consent <url> first", shown(filepath.Join(st.Root, store.Consent)))
		}
		return readResult(against, filepath.Join(dir, evidence.ResultFile))
	}
	if fi, err := os.Stat(against); err == nil {
		path := against
		if fi.IsDir() {
			path = filepath.Join(against, evidence.ResultFile)
			if _, err := os.Stat(path); err != nil {
				return nil, usagef("--against %s: no %s in that folder", against, evidence.ResultFile)
			}
		}
		return readResult(against, path)
	}
	if store.CheckName(against) == nil {
		path := filepath.Join(baselinesDir(st), against, evidence.ResultFile)
		if _, err := os.Stat(path); err == nil {
			return readResult(against, path)
		}
	}
	return nil, usagef("--against %q: no saved consent result by that name in %s; rhythm echo consent <url> --save %s makes one",
		against, shown(baselinesDir(st)), saveHint(against))
}

// saveHint is the name the message 5 example saves under: the one
// asked for when it can be a name.
func saveHint(name string) string {
	if store.CheckName(name) == nil {
		return name
	}
	return "baseline"
}

// readResult reads a consent.json for --against named what.
func readResult(what, path string) (*consent.Result, error) {
	f, err := os.Open(path) //nolint:gosec // a result the user names
	if err != nil {
		return nil, usagef("--against %s: %v", what, err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxResultFile+1))
	if err != nil {
		return nil, usagef("--against %s: %v", what, err)
	}
	if len(data) > maxResultFile {
		return nil, usagef("--against %s: %s is more than 64 MiB; that isn't a consent result", what, shown(path))
	}
	var r consent.Result
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, usagef("--against %s: %s isn't a consent result: %v", what, shown(path), err)
	}
	return &r, nil
}

// latestResult is the newest result folder in the store that holds a
// consent.json (folder names start with their time, so the newest
// sorts last), or "" when there is none.
func latestResult(st store.Store) (string, error) {
	dirs, err := resultDirs(st)
	if err != nil || len(dirs) == 0 {
		return "", err
	}
	return dirs[len(dirs)-1], nil
}

// resultDirs lists the store's consent result folders that hold a
// consent.json, oldest first; never baselines/ or detect/.
func resultDirs(st store.Store) ([]string, error) {
	root := filepath.Join(st.Root, store.Consent)
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || name == "baselines" || name == "detect" || strings.HasPrefix(name, ".") {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, name, evidence.ResultFile)); err == nil {
			out = append(out, filepath.Join(root, name))
		}
	}
	slices.Sort(out)
	return out, nil
}

// saveBaseline keeps the consent.json of dir as the baseline name and
// returns the line that says so: a baseline of that name is replaced,
// and the line names the date of the one it replaced.
func saveBaseline(st store.Store, name, dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, evidence.ResultFile)) //nolint:gosec // the result just written
	if err != nil {
		return "", err
	}
	dest := filepath.Join(baselinesDir(st), name)
	line := fmt.Sprintf("Saved baseline %s.", name)
	if old, err := readResult(name, filepath.Join(dest, evidence.ResultFile)); err == nil {
		line = fmt.Sprintf("Saved baseline %s (replaced the one from %s).", name, old.Started.UTC().Format("2006-01-02 15:04"))
	}
	// The parents get the store's 0700 like every consent folder.
	for _, d := range []string{filepath.Join(st.Root, store.Consent), baselinesDir(st), dest} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return "", err
		}
	}
	if err := cache.WriteFile(filepath.Join(dest, evidence.ResultFile), data); err != nil {
		return "", err
	}
	return line, nil
}

// shown is path as messages show it: relative to the working folder
// when it is below it, with forward slashes on every system, as the
// evidence folder is named in consent.json.
func shown(path string) string {
	wd, err := os.Getwd()
	if err != nil {
		return filepath.ToSlash(path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	if r, err := filepath.Rel(wd, abs); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return filepath.ToSlash(path)
}
