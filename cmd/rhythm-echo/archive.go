// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/report"
	"github.com/RhythmShahriar/echo/store"
)

// Limits on what an archive may hold. A snapshot is a run.json and an
// HTML report, a few MB at most; the limits stop a crafted archive from
// filling the disk or keeping Echo busy.
const (
	archiveMaxFiles = 100
	archiveMaxFile  = 64 << 20
	archiveMaxTotal = 128 << 20
)

// archiveKeeps are the files unpackSnapshot writes. Anything else in an
// archive is skipped: a report.html from someone else's archive would
// run its scripts when opened from disk, so import writes its own.
var archiveKeeps = []string{"run.json", "comparison.json"}

// exportSnapshot writes the snapshot in dir to w as a gzipped tar, every
// file under name/ (run.json, report.html and anything else saved with
// it). Entries are 0600 and owned by no one: the files hold captured
// messages, so whoever unpacks them shouldn't get them world-readable.
func exportSnapshot(w io.Writer, dir, name string) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	err := filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		inside := path.Join(name, filepath.ToSlash(relPath))
		info, err := e.Info()
		if err != nil {
			return err
		}
		switch {
		case e.IsDir():
			return tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: inside + "/", Mode: 0o700, ModTime: info.ModTime()})
		case !info.Mode().IsRegular():
			return nil // nothing a snapshot saves; a link could point anywhere
		}
		f, err := os.Open(p) //nolint:gosec // a file in our own snapshot folder
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: inside, Mode: 0o600, Size: info.Size(), ModTime: info.ModTime()}); err != nil {
			return err
		}
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// errArchive marks an archive that isn't a snapshot export: the user's
// to fix, so it exits 2.
var errArchive = errors.New("not a snapshot archive")

// unpackSnapshot unpacks the run.json (and comparison.json) of an archive
// from exportSnapshot into dest (which must exist) and returns the
// snapshot's name in it. Every entry must sit under one top folder with
// a valid snapshot name, be a plain file or folder and stay inside dest:
// absolute paths, .. and links are refused before anything is written
// outside dest. Other entries are checked the same way, then skipped.
func unpackSnapshot(r io.Reader, dest string) (string, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return "", fmt.Errorf("%w: %w", errArchive, err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	top, files, total := "", 0, int64(0)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("%w: %w", errArchive, err)
		}
		name := h.Name
		clean := path.Clean(name)
		first, rest, _ := strings.Cut(clean, "/")
		switch {
		case strings.Contains(name, `\`) || path.IsAbs(name) || slices.Contains(strings.Split(name, "/"), ".."):
			return "", fmt.Errorf("%w: %q points outside the snapshot", errArchive, name)
		case !store.ValidName(first) || rest == "" && h.Typeflag != tar.TypeDir:
			return "", fmt.Errorf("%w: %q isn't in a snapshot folder", errArchive, name)
		case top != "" && first != top:
			return "", fmt.Errorf("%w: it holds more than one snapshot (%s and %s)", errArchive, top, first)
		}
		top = first
		switch h.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg:
		default:
			return "", fmt.Errorf("%w: %q isn't a plain file", errArchive, name)
		}
		files++
		total += h.Size
		if files > archiveMaxFiles || h.Size > archiveMaxFile || total > archiveMaxTotal {
			return "", fmt.Errorf("%w: it's larger than a snapshot can be", errArchive)
		}
		if !slices.Contains(archiveKeeps, rest) {
			continue
		}
		if err := writeEntry(filepath.Join(dest, rest), tr, h.Size); err != nil {
			return "", err
		}
	}
	if top == "" {
		return "", fmt.Errorf("%w: it's empty", errArchive)
	}
	return top, nil
}

// writeEntry writes size bytes from r to a new 0600 file at target.
func writeEntry(target string, r io.Reader, size int64) error {
	f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // checked to be inside dest
	if err != nil {
		return err
	}
	if _, err := io.CopyN(f, r, size); err != nil {
		_ = f.Close()
		return fmt.Errorf("%w: %w", errArchive, err)
	}
	return f.Close()
}

// importSnapshot adds the snapshot in the archive at file to st as name
// (or, when name is "", under the name it was exported with) and makes it
// the latest. An existing snapshot of that name is replaced only when
// replace is set. The archive is unpacked next to the snapshots and
// checked to hold a run Echo can read before it takes a snapshot's place,
// and its HTML report is written anew from that run.
func importSnapshot(st store.Store, file, name string, replace bool) (string, error) {
	f, err := os.Open(file) //nolint:gosec // the user names the archive
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	tmp, err := st.NewDir(store.Incoming, "import-"+store.Stamp(time.Now()))
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }() //nolint:gosec // our own incoming folder
	top, err := unpackSnapshot(f, tmp)
	if err != nil {
		return "", err
	}
	run, err := store.LoadRun(tmp)
	if err != nil {
		return "", fmt.Errorf("%w: %w", errArchive, err)
	}
	if name == "" {
		name = top
	}
	if !replace {
		if _, err := os.Stat(filepath.Join(st.Root, "snapshots", name)); err == nil {
			return "", usageErr("snapshot %s already exists (--force replaces it, --name imports it under another name)", name)
		}
	}
	if run.Name != name {
		run.Name = name // reports and compare name the snapshot by it
		if err := store.SaveRun(tmp, run); err != nil {
			return "", err
		}
	}
	if err := writeFile(filepath.Join(tmp, "report.html"), func(w io.Writer) error {
		return report.HTML(w, report.Result{Run: run, Dir: tmp})
	}); err != nil {
		return "", err
	}
	if _, err := st.Promote(tmp, name); err != nil {
		return "", err
	}
	if err := st.SetLatest(name); err != nil {
		return "", err
	}
	st.ExcludeFromGit()
	return name, nil
}

// loadBaseline reads the run compare --against names: a run or snapshot
// folder, a run.json (or any file holding one) or an archive from
// snapshot --export, so CI can compare with a baseline from another
// checkout or job.
func loadBaseline(p string) (*model.Run, error) {
	fi, err := os.Stat(p)
	if err != nil {
		return nil, usageErr("--against %s: %v", p, err)
	}
	lower := strings.ToLower(p)
	switch {
	case fi.IsDir():
		return store.LoadRun(p)
	case strings.HasSuffix(lower, ".tgz") || strings.HasSuffix(lower, ".tar.gz"):
		tmp, err := os.MkdirTemp("", "rhythm-echo-baseline-")
		if err != nil {
			return nil, err
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		f, err := os.Open(p) //nolint:gosec // the user names the archive
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		if _, err := unpackSnapshot(f, tmp); err != nil {
			return nil, err
		}
		return store.LoadRun(tmp)
	case filepath.Base(p) == "run.json":
		return store.LoadRun(filepath.Dir(p))
	}
	b, err := os.ReadFile(p) //nolint:gosec // the user names the file
	if err != nil {
		return nil, err
	}
	var r model.Run
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, usageErr("--against %s: not a run (%v)", p, err)
	}
	if r.Schema != model.Schema {
		return nil, usageErr("--against %s: not a run Echo can read (schema %q); take a new snapshot", p, r.Schema)
	}
	return &r, nil
}
