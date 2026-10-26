// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package evidence

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/rhythm/kit/cache"
)

// The files of an evidence folder this package writes.
const (
	ResultFile   = "consent.json"
	ManifestFile = "manifest.json"
)

// ManifestSchema names the manifest's shape.
const ManifestSchema = "rhythm-echo-consent-manifest/v1"

// attachments are the folders whose files consent.json lists by hash:
// they are written before it, by the runs.
var attachments = []string{"events", "evidence", "har"}

// Manifest is manifest.json: every file of the folder but itself.
type Manifest struct {
	Schema string                 `json:"schema"`
	Files  []consent.EvidenceFile `json:"files"`
}

// WriteResult hashes every attachment already in dir (events/,
// evidence/, har/) into r.Evidence.Files, then writes consent.json
// atomically (0600 in a 0700 folder) as the bare result and returns its
// SHA-256, which the reports then state. r.Evidence.Folder is set to
// dir when the caller left it empty. signKey, when set, is the
// --sign-key file: consent.json is then signed into consent.json.sig.
// The key is read before anything is written, so a bad key never
// leaves an unsigned result the user believes signed.
func WriteResult(dir string, r *consent.Result, signKey string) (sha string, err error) {
	var key ed25519.PrivateKey
	if signKey != "" {
		if key, err = LoadSignKey(signKey); err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	files, err := hashFiles(dir, func(rel string) bool {
		top, _, _ := strings.Cut(rel, "/")
		return slices.Contains(attachments, top)
	})
	if err != nil {
		return "", err
	}
	if r.Evidence.Folder == "" {
		r.Evidence.Folder = filepath.ToSlash(dir)
	}
	r.Evidence.Files = files
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	if err := cache.WriteFile(filepath.Join(dir, ResultFile), data); err != nil {
		return "", err
	}
	if key != nil {
		if err := sign(dir, key, data); err != nil {
			return "", err
		}
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// WriteManifest writes manifest.json: the path, size and SHA-256 of
// every file in dir but itself, sorted by path. It is written last, so
// it covers consent.json, its signature and the reports.
func WriteManifest(dir string) error {
	files, err := hashFiles(dir, func(rel string) bool { return rel != ManifestFile })
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(Manifest{Schema: ManifestSchema, Files: files}, "", "  ")
	if err != nil {
		return err
	}
	return cache.WriteFile(filepath.Join(dir, ManifestFile), append(data, '\n'))
}

// hashFiles hashes the regular files under dir that keep accepts (by
// their path relative to dir, with forward slashes), sorted by path.
// Links are never followed: the folder is Echo's own, so one there
// isn't evidence. Temporary files of an atomic write are skipped.
func hashFiles(dir string, keep func(rel string) bool) ([]consent.EvidenceFile, error) {
	files := []consent.EvidenceFile{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() || strings.HasPrefix(d.Name(), ".tmp-") {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !keep(rel) {
			return nil
		}
		f, err := HashFile(path)
		if err != nil {
			return err
		}
		f.Path = rel
		files = append(files, f)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("hashing the evidence: %w", err)
	}
	slices.SortFunc(files, func(a, b consent.EvidenceFile) int { return strings.Compare(a.Path, b.Path) })
	return files, nil
}

// HashFile returns the size and SHA-256 of the file at path (Path is
// left for the caller).
func HashFile(path string) (consent.EvidenceFile, error) {
	f, err := os.Open(path) //nolint:gosec // a file of Echo's own evidence folder
	if err != nil {
		return consent.EvidenceFile{}, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return consent.EvidenceFile{}, err
	}
	return consent.EvidenceFile{Bytes: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}
