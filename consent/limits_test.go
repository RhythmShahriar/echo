// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package consent_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
)

// plainSection turns PRD section 15 (a Markdown blockquote) into the
// plain text HonestLimits holds: the quote markers and ** dropped, each
// paragraph on one line, a blank line between paragraphs.
func plainSection(md string) string {
	var paras []string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			paras = append(paras, strings.Join(cur, " "))
			cur = nil
		}
	}
	for line := range strings.SplitSeq(strings.ReplaceAll(md, "\r\n", "\n"), "\n") {
		if !strings.HasPrefix(line, ">") {
			continue
		}
		t := strings.TrimSpace(strings.TrimPrefix(line, ">"))
		if t == "" {
			flush()
			continue
		}
		cur = append(cur, strings.ReplaceAll(t, "**", ""))
	}
	flush()
	return strings.Join(paras, "\n\n")
}

// HonestLimits is PRD 15 word for word: the copy in testdata; also the
// PRD itself where the workspace has it (it is kept out of the repo).
func TestHonestLimitsMatchPRD(t *testing.T) {
	md, err := os.ReadFile(filepath.Join("testdata", "prd-15.md"))
	if err != nil {
		t.Fatal(err)
	}
	if got := plainSection(string(md)); got != consent.HonestLimits {
		t.Errorf("HonestLimits differs from testdata/prd-15.md:\n got %q\nwant %q", consent.HonestLimits, got)
	}
	if n := strings.Count(consent.HonestLimits, "\n\n") + 1; n != 9 {
		t.Errorf("HonestLimits has %d paragraphs; want 9", n)
	}
	prd, err := os.ReadFile(filepath.Join("..", "..", "review", "2026-10b", "echo-consent", "PRD.md"))
	if os.IsNotExist(err) {
		t.Log("no workspace PRD next to this repository; compared with testdata only")
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	s := string(prd)
	start := strings.Index(s, "\n## 15. ")
	end := strings.Index(s, "\n## 16. ")
	if start < 0 || end < start {
		t.Fatal("PRD has no section 15")
	}
	if got := plainSection(s[start:end]); got != consent.HonestLimits {
		t.Errorf("HonestLimits differs from PRD 15; update limits.go and testdata/prd-15.md:\n got %q\nwant %q", consent.HonestLimits, got)
	}
}
