// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verdict

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/RhythmShahriar/echo/consent"
)

// name is a tracker's name for titles: from its hits, else the catalog,
// else its id.
func (j *judge) name(id string) string {
	if n := j.names[id]; n != "" {
		return n
	}
	if t, ok := j.info[id]; ok && t.Name != "" {
		return t.Name
	}
	return id
}

// trackerFix is the tracker's own fix line, else the check's.
func (j *judge) trackerFix(id, check string) string {
	if t, ok := j.info[id]; ok && t.Fix != "" {
		return t.Fix
	}
	return checkFix(check)
}

// checkFix is the catalog's fix line for check.
func checkFix(check string) string {
	c, _ := consent.Check(check)
	return c.Fix
}

// maxEvidence caps the event numbers one issue lists; the detail then
// says how many there were.
const maxEvidence = 50

// add appends an issue with its key, default level, policy and site.
func (j *judge) add(pg *consent.Page, check string, run consent.RunKind, tracker, title, detail, fix string, evidence []int64) {
	c, _ := consent.Check(check)
	if fix == "" {
		fix = c.Fix
	}
	if n := len(evidence); n > maxEvidence {
		evidence = evidence[:maxEvidence]
		detail += fmt.Sprintf("; %d of %d events listed", maxEvidence, n)
	}
	if evidence == nil {
		evidence = []int64{}
	}
	path := pagePath(pg.URL)
	j.r.Issues = append(j.r.Issues, consent.Issue{
		Check: check, Level: c.Level, Key: consent.IssueKey(check, path, run, tracker),
		Page: path, Site: pg.Site, Run: run, Tracker: tracker, Policy: j.policy,
		Title: title, Detail: detail, Fix: fix, Evidence: evidence,
	})
}

// pagePath is the path of a page URL, the way issues name and compare
// match pages. The query is dropped: it often changes between runs
// (campaign tags, cache busters) and may carry values the summary
// mustn't show. Text that isn't a URL with a host is kept whole.
func pagePath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	p := u.EscapedPath()
	if p == "" {
		p = "/"
	}
	return p
}

// visitPath is the path of the run's visit i, or "" when there is none.
func visitPath(run *consent.Run, i int) string {
	if i < 0 || i >= len(run.Visits) {
		return ""
	}
	return pagePath(run.Visits[i].URL)
}

// secs writes a run clock time as seconds with two decimals: 0.84s.
func secs(ms int64) string {
	return fmt.Sprintf("%.2fs", float64(ms)/1000)
}

// joinAnd lists words as "a", "a and b" or "a, b and c".
func joinAnd(words []string) string {
	switch len(words) {
	case 0:
		return ""
	case 1:
		return words[0]
	}
	return strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
}

// seqOfCookie finds the event number of a recorded cookie in the run's
// events; 0 when the events aren't in memory.
func seqOfCookie(run *consent.Run, c *consent.Cookie) int64 {
	for _, ev := range run.Events {
		if ev.Kind == consent.EvCookie && ev.Cookie != nil && ev.Cookie.Name == c.Name && ev.Cookie.AtMS == c.AtMS {
			return ev.Seq
		}
	}
	return 0
}

// seqOfStorage finds the event number of a recorded storage key.
func seqOfStorage(run *consent.Run, k *consent.StorageKey) int64 {
	for _, ev := range run.Events {
		if ev.Kind == consent.EvStorage && ev.Storage != nil && ev.Storage.Key == k.Key && ev.Storage.AtMS == k.AtMS {
			return ev.Seq
		}
	}
	return 0
}
