// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package scenarios

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzParse feeds the manifest decoder broken and odd YAML: it must
// return an error or a manifest that passed its own checks, never panic.
func FuzzParse(f *testing.F) {
	if data, err := os.ReadFile(filepath.Join(root, File)); err == nil {
		f.Add(data)
	}
	f.Add([]byte("scenarios:\n  - {id: g01, name: x, area: cmp, cmp: x, needs: go, toggle: x, allow: []}\n"))
	f.Add([]byte("scenarios:\n  - {id: c01, name: x, area: cmp, cmp: x, needs: go, toggle: x, expect: [{check: a}]}\n"))
	f.Add([]byte("scenarios: [&a {id: c01}, *a]\n"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		m, err := Parse(data)
		if err != nil {
			return
		}
		seen := map[string]bool{}
		for _, s := range m.Scenarios {
			if seen[s.ID] {
				t.Fatalf("duplicate id %q passed the checks", s.ID)
			}
			seen[s.ID] = true
			if !s.Good() && len(s.Expect) == 0 {
				t.Fatalf("bad scenario %q without expectations passed the checks", s.ID)
			}
		}
		_ = m.Sites()
	})
}
