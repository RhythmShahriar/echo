// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package action

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestVersion checks which Echo the Action installs for each way it can
// be used.
func TestVersion(t *testing.T) {
	needBash(t, "jq")
	sh := filepath.Join(root(t), "integrations", "action", "version.sh")
	const sha = "4f1c2a6b8d0e1f2a3b4c5d6e7f8091a2b3c4d5e6"
	tags := `[{"name": "v1", "commit": {"sha": "` + sha + `"}}, {"name": "v1.4.0", "commit": {"sha": "` + sha + `"}}, {"name": "v1.3.0", "commit": {"sha": "0000000000000000000000000000000000000000"}}]`
	g := fakeGH(t, tags, "")
	path := "PATH=" + g.bin + ":" + os.Getenv("PATH")
	for _, tc := range []struct {
		name    string
		env     []string
		want    string
		warning string
	}{
		{"version set", []string{"VERSION=1.2.0", "ACTION_REF=v1.4.0"}, "1.2.0", ""},
		{"release tag", []string{"ACTION_REF=v1.4.0"}, "v1.4.0", ""},
		{"commit of a release", []string{"ACTION_REF=" + sha}, "v1.4.0", ""},
		{"commit of no release", []string{"ACTION_REF=1111111111111111111111111111111111111111"}, "latest", "no release tag points at 1111111111111111111111111111111111111111"},
		{"branch", []string{"ACTION_REF=main"}, "latest", "isn't used by a release tag or its commit"},
		{"major tag", []string{"ACTION_REF=v1"}, "latest", "isn't used by a release tag or its commit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, errOut, code := script(t, sh, append([]string{path, "GH_TOKEN=x"}, tc.env...))
			if code != 0 || strings.TrimSpace(out) != tc.want {
				t.Fatalf("exit %d, got %q want %q: %s", code, out, tc.want, errOut)
			}
			if tc.warning == "" && errOut != "" || !strings.Contains(errOut, tc.warning) {
				t.Errorf("stderr %q, want %q", errOut, tc.warning)
			}
		})
	}

	// The API can't be reached (gh fails): latest, with the warning.
	if err := os.WriteFile(filepath.Join(g.bin, "fail"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	out, errOut, code := script(t, sh, []string{path, "ACTION_REF=" + sha})
	if code != 0 || strings.TrimSpace(out) != "latest" || !strings.Contains(errOut, "no release tag points at") {
		t.Errorf("gh failing: exit %d %q %q", code, out, errOut)
	}
}
