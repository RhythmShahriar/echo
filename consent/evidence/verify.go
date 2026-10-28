// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package evidence

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/report"
)

// maxManifest caps manifest.json and consent.json.sig as verify reads
// them: a folder handed over by someone else may hold anything.
const (
	maxManifest  = 8 << 20
	maxSignature = 64 << 10
)

// UnreadableError is a folder verify can't check at all: no manifest,
// or one it can't read. consent verify exits 3 for it.
type UnreadableError struct{ Msg string }

func (e *UnreadableError) Error() string { return e.Msg }

// Folder is the evidence folder target names: target itself, or the
// folder of a consent.json (or any file in the folder) given instead.
func Folder(target string) string {
	if fi, err := os.Stat(target); err == nil && !fi.IsDir() {
		return filepath.Dir(target)
	}
	return target
}

// Verify checks the evidence folder dir (or the folder of the file dir
// names): it recomputes the SHA-256 of every file manifest.json lists,
// checks that consent.json is among them and that the hashes it lists
// for its attachments agree; it also checks consent.json.sig when present.
// pubKeyPath is --key: with it the signature is checked (and one that
// is missing fails); without it a signature is only reported as there
// (Signature.Key ""). A key that can't be read is a usage error; a
// folder with no readable manifest is an *UnreadableError.
//
// Each file's Got is "" when the file is missing or can't be read;
// OK is false when Got differs from Want.
func Verify(dir, pubKeyPath string) (*report.ConsentVerify, error) {
	dir = Folder(dir)
	var pub ed25519.PublicKey
	if pubKeyPath != "" {
		var err error
		if pub, err = LoadPublicKey(pubKeyPath); err != nil {
			return nil, err
		}
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return nil, &UnreadableError{fmt.Sprintf("consent verify %s: %v", dir, unwrapPath(err))}
	}
	if !fi.IsDir() {
		return nil, &UnreadableError{fmt.Sprintf("consent verify %s: not a folder", dir)}
	}
	m, err := readManifest(dir)
	if err != nil {
		return nil, err
	}
	v := &report.ConsentVerify{Folder: filepath.ToSlash(dir), Files: []report.VerifyFile{}}
	got := map[string]string{} // path: hash, "" when unreadable
	hash := func(rel string) string {
		if h, ok := got[rel]; ok {
			return h
		}
		h := ""
		if f, err := hashRegular(dir, rel); err == nil {
			h = f.SHA256
		}
		got[rel] = h
		return h
	}
	listed := map[string]string{}
	at := map[string]int{} // path: its entry in v.Files
	// add checks rel against want: one entry per path, where a failed
	// check replaces a passed one (the manifest and consent.json can
	// hold different hashes for a file; either not matching is a change).
	add := func(rel, want string) {
		g := hash(rel)
		f := report.VerifyFile{Path: rel, OK: g != "" && g == want, Want: want, Got: g}
		if i, ok := at[rel]; ok {
			if v.Files[i].OK && !f.OK {
				v.Files[i] = f
			}
			return
		}
		at[rel] = len(v.Files)
		v.Files = append(v.Files, f)
	}
	for _, f := range m.Files {
		listed[f.Path] = f.SHA256
		add(f.Path, f.SHA256)
	}
	if _, ok := listed[ResultFile]; !ok {
		// A manifest without consent.json covers none of the evidence of
		// record: consent.json is checked anyway, against nothing.
		at[ResultFile] = len(v.Files)
		v.Files = append(v.Files, report.VerifyFile{Path: ResultFile, Want: "", Got: hash(ResultFile)})
	}
	data, readErr := readCapped(filepath.Join(dir, ResultFile), maxManifest*4)
	if readErr == nil {
		var r consent.Result
		if json.Unmarshal(data, &r) == nil {
			for _, f := range r.Evidence.Files {
				if !safePath(f.Path) {
					continue
				}
				if want, ok := listed[f.Path]; ok && want == f.SHA256 {
					continue // checked above with the same hash
				}
				add(f.Path, f.SHA256)
			}
		}
	}
	sig, sigErr := readCapped(filepath.Join(dir, SignatureFile), maxSignature)
	v.Signature.Present = sigErr == nil
	if pub != nil {
		v.Signature.Key = pubKeyPath
		v.Signature.OK = v.Signature.Present && readErr == nil && checkSignature(sig, pub, data)
	}
	return v, nil
}

// readManifest reads dir's manifest.json, refusing one that names a
// file outside the folder.
func readManifest(dir string) (*Manifest, error) {
	data, err := readCapped(filepath.Join(dir, ManifestFile), maxManifest)
	if errors.Is(err, os.ErrNotExist) {
		return nil, &UnreadableError{fmt.Sprintf("consent verify %s: no %s; is it a consent result folder?", dir, ManifestFile)}
	}
	if err != nil {
		return nil, &UnreadableError{fmt.Sprintf("consent verify %s: %s: %v", dir, ManifestFile, unwrapPath(err))}
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil || m.Schema != ManifestSchema {
		return nil, &UnreadableError{fmt.Sprintf("consent verify %s: %s isn't a consent manifest (%s)", dir, ManifestFile, ManifestSchema)}
	}
	for _, f := range m.Files {
		if !safePath(f.Path) {
			return nil, &UnreadableError{fmt.Sprintf("consent verify %s: %s lists %q, which is outside the folder", dir, ManifestFile, consent.SafeText(f.Path, 120))}
		}
	}
	return &m, nil
}

// safePath reports whether rel is a plain relative path inside the
// folder: forward slashes, no "..", not absolute.
func safePath(rel string) bool {
	if rel == "" || strings.Contains(rel, `\`) || strings.ContainsRune(rel, 0) || path.IsAbs(rel) || filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" {
		return false
	}
	clean := path.Clean(rel)
	return clean == rel && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

// hashRegular hashes the file rel inside dir when it is a regular file
// whose folders resolve inside dir: a link is never followed, as the
// manifest never lists one. A linked folder (events -> /etc) in a
// folder handed over by someone else would read a file outside it.
func hashRegular(dir, rel string) (consent.EvidenceFile, error) {
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := insideFolder(dir, filepath.Dir(p)); err != nil {
		return consent.EvidenceFile{}, err
	}
	fi, err := os.Lstat(p)
	if err != nil {
		return consent.EvidenceFile{}, err
	}
	if !fi.Mode().IsRegular() {
		return consent.EvidenceFile{}, errors.New("not a regular file")
	}
	return HashFile(p)
}

// insideFolder fails when sub, with its links resolved, isn't dir or a
// folder under it.
func insideFolder(dir, sub string) error {
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(sub)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return errors.New("outside the folder")
	}
	return nil
}

// Unlisted lists the files in the evidence folder dir that neither
// manifest.json nor consent.json covers (v is Verify's result), as
// slash paths in walk order. Neither the hashes nor the signature say
// anything about them, so consent verify names them. Links are listed,
// never followed.
func Unlisted(dir string, v *report.ConsentVerify) []string {
	dir = Folder(dir)
	known := map[string]bool{ManifestFile: true, SignatureFile: true, SigstoreFile: true}
	for _, f := range v.Files {
		known[f.Path] = true
	}
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // an unreadable folder is skipped, as missing files are reported already
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return nil //nolint:nilerr // can't happen under WalkDir's root
		}
		if rel = filepath.ToSlash(rel); !known[rel] {
			out = append(out, rel)
		}
		return nil
	})
	return out
}

// readCapped reads a regular file of at most limit bytes.
func readCapped(p string, limit int64) ([]byte, error) {
	fi, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	f, err := os.Open(p) //nolint:gosec // a file of the evidence folder being verified
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("bigger than %d bytes", limit)
	}
	return data, nil
}
