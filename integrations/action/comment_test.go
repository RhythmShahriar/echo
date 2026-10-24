// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package action

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// gh is a fake GitHub CLI: it records each call's arguments (one file per
// call) and answers a comment listing with list and a POST with postID.
type gh struct{ bin string }

func fakeGH(t *testing.T, list, postID string) gh {
	t.Helper()
	g := gh{bin: t.TempDir()}
	g.reply(t, list, postID)
	script := `#!/usr/bin/env bash
dir=$(dirname "$0")
n=$(ls "$dir"/call.* 2>/dev/null | wc -l | tr -d ' ')
printf '%s ' "$@" > "$dir/call.$(printf '%03d' "$n")"
[ -f "$dir/fail" ] && { echo "HTTP 403: Resource not accessible by integration" >&2; exit 1; }
case " $* " in
*" -X POST "*) cat "$dir/post-id" ;;
*" -X PATCH "*) echo '{}' ;;
*) cat "$dir/list.json" ;;
esac
`
	if err := os.WriteFile(filepath.Join(g.bin, "gh"), []byte(script), 0o700); err != nil { //nolint:gosec // a test executable
		t.Fatal(err)
	}
	return g
}

// reply sets the next answers and forgets the calls so far.
func (g gh) reply(t *testing.T, list, postID string) {
	t.Helper()
	old, _ := filepath.Glob(filepath.Join(g.bin, "call.*"))
	for _, f := range old {
		_ = os.Remove(f)
	}
	for name, body := range map[string]string{"list.json": list, "post-id": postID} {
		if err := os.WriteFile(filepath.Join(g.bin, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// calls returns each call's arguments, joined by spaces, in order.
func (g gh) calls(t *testing.T) []string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(g.bin, "call.*"))
	sort.Strings(files)
	var out []string
	for _, f := range files {
		b, err := os.ReadFile(f) //nolint:gosec // the fake's own file
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, strings.TrimSpace(string(b)))
	}
	return out
}

func TestComment(t *testing.T) {
	needBash(t, "jq")
	sh := filepath.Join(root(t), "integrations", "action", "comment.sh")
	body := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(body, []byte("## ♪ Echo\n\nhello"), 0o600); err != nil {
		t.Fatal(err)
	}
	g := fakeGH(t, `[{"id": 9, "body": "<!-- rhythm-echo comment: other -->"}]`, "77")
	output := filepath.Join(t.TempDir(), "out")
	env := []string{"PATH=" + g.bin + ":" + os.Getenv("PATH"), "GITHUB_REPOSITORY=o/r", "PR=3", "KEY=sites/shop", "GH_TOKEN=x", "GITHUB_OUTPUT=" + output}

	// Another site's comment isn't this one: post a new one.
	out, _, code := script(t, sh, env, body)
	if code != 0 || !strings.Contains(out, "Posted the pull request comment 77.") {
		t.Fatalf("exit %d: %s", code, out)
	}
	calls := g.calls(t)
	if len(calls) != 2 || calls[1] != "api -X POST repos/o/r/issues/3/comments -f body=<!-- rhythm-echo comment: sites/shop -->\n## ♪ Echo\n\nhello --jq .id" {
		t.Errorf("calls: %q", calls)
	}
	if b, _ := os.ReadFile(output); string(b) != "id=77\n" { //nolint:gosec // the test's own file
		t.Errorf("GITHUB_OUTPUT = %q", b)
	}

	// The next run finds it by its marker and edits it.
	g.reply(t, `[{"id": 9, "body": "<!-- rhythm-echo comment: other -->"}]`+"\n"+`[{"id": 77, "body": "<!-- rhythm-echo comment: sites/shop -->\nold"}]`, "")
	out, _, code = script(t, sh, env, body)
	if code != 0 || !strings.Contains(out, "Updated the pull request comment 77.") {
		t.Fatalf("exit %d: %s", code, out)
	}
	if calls := g.calls(t); len(calls) != 2 || !strings.HasPrefix(calls[1], "api -X PATCH repos/o/r/issues/comments/77 ") {
		t.Errorf("calls: %q", calls)
	}

	// A fork's read-only token: warn, don't fail the job.
	if err := os.WriteFile(filepath.Join(g.bin, "fail"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, code = script(t, sh, env, body)
	if code != 0 || !strings.Contains(out, `::warning::Couldn't read the pull request comment. The job needs "permissions: pull-requests: write"`) {
		t.Errorf("exit %d: %s", code, out)
	}
}
