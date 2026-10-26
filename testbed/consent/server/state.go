// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package server

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// idPattern is what a scenario id looks like: c01, g12, ssrf-probe.
var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)

func validID(id string) bool { return idPattern.MatchString(id) }

// errUnknownScenario is returned for an id with no site folder.
var errUnknownScenario = errors.New("no such scenario")

// Broken reports whether scenario id is in its broken state: the file
// state/<id> exists. It is read on every request, so toggle.sh, the
// admin endpoint and a file written by hand all take effect at once.
func (s *Server) Broken(id string) bool {
	if !validID(id) {
		return false
	}
	_, err := os.Stat(filepath.Join(s.stateDir, id)) //nolint:gosec // id is checked against idPattern above
	return err == nil
}

// SetBroken switches scenario id to its broken (true) or good state, by
// writing or removing state/<id> as toggle.sh does.
func (s *Server) SetBroken(id string, broken bool) error {
	if !s.HasSite(id) {
		return errUnknownScenario
	}
	p := filepath.Join(s.stateDir, id)
	if !broken {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) { //nolint:gosec // HasSite checked id
			return err
		}
		return nil
	}
	if err := os.MkdirAll(s.stateDir, 0o750); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600) //nolint:gosec // HasSite checked id
}

// HasSite reports whether id names a scenario site folder.
func (s *Server) HasSite(id string) bool {
	if !validID(id) {
		return false
	}
	fi, err := os.Stat(filepath.Join(s.sitesDir, id)) //nolint:gosec // id is checked against idPattern above
	return err == nil && fi.IsDir()
}

// Sites lists the scenario site folders, sorted.
func (s *Server) Sites() ([]string, error) {
	entries, err := os.ReadDir(s.sitesDir)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() && validID(e.Name()) {
			ids = append(ids, e.Name())
		}
	}
	return ids, nil
}
