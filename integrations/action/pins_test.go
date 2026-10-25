// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package action

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var (
	usesLine = regexp.MustCompile(`^\s*(?:-\s+)?uses:\s*(\S+)(.*)$`)
	pinned   = regexp.MustCompile(`^[\w.-]+/[\w./-]+@[0-9a-f]{40}$`)
	version  = regexp.MustCompile(`^\s*#\s*\S+`)
)

// TestUsesPinned checks every uses: in the Action and the workflows names
// a full commit SHA with the version in a comment, so a moved tag can't
// change what runs and Dependabot knows what to bump.
// releaseWorkflow is the shared release workflow, called at master on
// purpose (see .github/workflows/release.yml).
const releaseWorkflow = "RhythmShahriar/.github/.github/workflows/release-go.yml@master"

func TestUsesPinned(t *testing.T) {
	r := root(t)
	files, err := filepath.Glob(filepath.Join(r, ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, filepath.Join(r, "action.yml"))
	n := 0
	for _, f := range files {
		b, err := os.ReadFile(f) //nolint:gosec // the repository's own files
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			m := usesLine.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			n++
			ref, rest := m[1], m[2]
			at := filepath.Base(f) + ":" + strconv.Itoa(i+1)
			switch {
			case strings.HasPrefix(ref, "./"):
			case ref == releaseWorkflow:
				// The one ref that must not be a SHA: the release is signed by
				// this workflow, and rhythm's verifier trusts its certificate
				// only for release-go.yml@refs/heads/master.
			case strings.HasPrefix(ref, "docker://"):
				if !strings.Contains(ref, "@sha256:") {
					t.Errorf("%s: %s isn't pinned by digest", at, ref)
				}
			case !pinned.MatchString(ref):
				t.Errorf("%s: %s isn't pinned by a full commit SHA", at, ref)
			case !version.MatchString(rest):
				t.Errorf("%s: %s has no version comment", at, ref)
			}
		}
	}
	if n == 0 {
		t.Fatal("found no uses: lines")
	}
}
