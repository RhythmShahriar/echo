package verdict

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/model"
)

// The result notes the verdict writes, in the order Notes lists them.
const (
	NoteNotLegalAdvice = "Evidence, not legal advice."
	NoteHeld           = "Hits held in the browser."
	NoteSent           = "Tracker hits were sent for real (--send-hits)."
	NoteTLS            = "TLS not verified."
	NoteNoSandbox      = "Chrome ran without its sandbox."
	NoteMapped         = "Hosts mapped with --map-host."
)

// geoNote is the note for --geo cc: the flag changes what the browser
// says about itself, never where its traffic comes from.
func geoNote(cc string) string {
	return "--geo " + cc + " set the language and time zone, not the IP address."
}

// ownNote reports whether note is one Evaluate writes, so judging a
// result again rebuilds it instead of keeping a stale copy.
func ownNote(note string) bool {
	switch note {
	case NoteNotLegalAdvice, NoteHeld, NoteSent, NoteTLS, NoteNoSandbox, NoteMapped, GCDNote:
		return true
	}
	return strings.HasPrefix(note, "--geo ") && strings.HasSuffix(note, " set the language and time zone, not the IP address.") ||
		strings.HasSuffix(note, ": the TCF string after the choice couldn't be read, so consent/tcf-mismatch wasn't checked")
}

// clearNotes drops the notes an earlier Evaluate wrote and keeps the
// rest (notes browse adds, for one).
func (j *judge) clearNotes() {
	kept := []string{}
	for _, n := range j.r.Notes {
		if !ownNote(n) {
			kept = append(kept, n)
		}
	}
	j.r.Notes = kept
}

// finish is the last step of Evaluate: levels from checks:, ignore:
// entries, the issue order and the notes.
func (j *judge) finish() {
	j.ignoreExpired()
	j.applyLevels()
	j.applyIgnores()
	j.sortIssues()
	j.setNotes()
}

// applyLevels applies checks: levels: "off" removes the check's issues,
// any other level replaces theirs.
func (j *judge) applyLevels() {
	if j.p == nil || len(j.p.CheckLevels) == 0 {
		return
	}
	kept := j.r.Issues[:0]
	for _, is := range j.r.Issues {
		lvl, ok := j.p.CheckLevels[is.Check]
		switch {
		case !ok:
		case strings.EqualFold(string(lvl), "off"):
			continue
		default:
			is.Level = model.Level(strings.ToLower(string(lvl)))
		}
		kept = append(kept, is)
	}
	j.r.Issues = kept
}

// today is the plan clock's date in UTC, as YYYY-MM-DD compares.
func (j *judge) today() string {
	return j.now().UTC().Format(time.DateOnly)
}

// expired reports whether an ignore: entry's until date has passed.
// An until that isn't a date never applies: an issue hidden by a typo
// would stay hidden for good.
func (j *judge) expired(ig consent.Ignore) bool {
	until, err := time.Parse(time.DateOnly, strings.TrimSpace(ig.Until))
	if err != nil {
		return true
	}
	return until.Format(time.DateOnly) < j.today()
}

// ignoreExpired adds one consent/ignore-expired notice per ignore:
// entry whose until date has passed.
func (j *judge) ignoreExpired() {
	if j.p == nil {
		return
	}
	for _, ig := range j.p.Ignore {
		if !j.expired(ig) {
			continue
		}
		what := cmp.Or(ig.Tracker, ig.Page, "every page")
		c, _ := consent.Check(consent.CheckIgnoreExpired)
		detail := fmt.Sprintf("ignore: %s until %s", ig.Check, cmp.Or(strings.TrimSpace(ig.Until), "(no date)"))
		if ig.Reason != "" {
			detail += ", reason: " + ig.Reason
		}
		j.r.Issues = append(j.r.Issues, consent.Issue{
			Check: c.ID, Level: c.Level, Key: consent.IssueKey(c.ID, ig.Page, ig.Run, ig.Tracker),
			Page: ig.Page, Run: ig.Run, Tracker: ig.Tracker, Policy: j.policy,
			Title:  fmt.Sprintf("The ignore for %s on %s expired on %s; the issue counts again", ig.Check, what, strings.TrimSpace(ig.Until)),
			Detail: detail, Fix: c.Fix, Evidence: []int64{},
		})
	}
}

// matches reports whether ignore: entry ig names issue is: the same
// check, and the same tracker, page and run where the entry sets them. A
// page given as a whole URL matches by its path, the way issues name
// pages.
func matches(ig consent.Ignore, is consent.Issue) bool {
	if ig.Check != is.Check {
		return false
	}
	if ig.Tracker != "" && !strings.EqualFold(ig.Tracker, is.Tracker) {
		return false
	}
	if ig.Page != "" && pagePath(ig.Page) != is.Page {
		return false
	}
	return ig.Run == "" || ig.Run == is.Run
}

// applyIgnores marks the issues an ignore: entry still in date names.
// They stay in the list, in their place, and don't count.
func (j *judge) applyIgnores() {
	if j.p == nil {
		return
	}
	for i := range j.r.Issues {
		is := &j.r.Issues[i]
		is.Ignored = nil
		for _, ig := range j.p.Ignore {
			if !j.expired(ig) && matches(ig, *is) {
				ig := ig
				is.Ignored = &ig
				break
			}
		}
	}
}

// runRank orders runs as a check makes them: none, accept, reject, gpc,
// withdraw.
func runRank(k consent.RunKind) int {
	if i := slices.Index(consent.AllRuns, k); i >= 0 {
		return i
	}
	return len(consent.AllRuns)
}

// checkRank orders checks as the catalog lists them, for issues that
// tie on everything else.
func checkRank(id string) int {
	return slices.IndexFunc(consent.Checks, func(c consent.CheckInfo) bool { return c.ID == id })
}

// sortIssues orders issues by level, then page order, run order and
// tracker id. Issues of no page (an expired ignore for every page) go
// after the pages.
func (j *judge) sortIssues() {
	pageRank := func(is consent.Issue) int {
		for i, pg := range j.r.Pages {
			if pg.Site == is.Site && pagePath(pg.URL) == is.Page {
				return i
			}
		}
		return len(j.r.Pages)
	}
	slices.SortStableFunc(j.r.Issues, func(a, b consent.Issue) int {
		return cmp.Or(
			cmp.Compare(a.Level.Rank(), b.Level.Rank()),
			cmp.Compare(pageRank(a), pageRank(b)),
			cmp.Compare(runRank(a.Run), runRank(b.Run)),
			cmp.Compare(a.Tracker, b.Tracker),
			cmp.Compare(checkRank(a.Check), checkRank(b.Check)),
		)
	})
}

// setNotes writes Notes: the verdict's own notes in a fixed order, the
// gcd note, then every other note in the order it was added.
func (j *judge) setNotes() {
	notes := []string{NoteNotLegalAdvice}
	if j.r.Held {
		notes = append(notes, NoteHeld)
	} else {
		notes = append(notes, NoteSent)
	}
	if !j.r.TLSVerified {
		notes = append(notes, NoteTLS)
	}
	if !j.r.Sandbox {
		notes = append(notes, NoteNoSandbox)
	}
	if j.r.Mapped {
		notes = append(notes, NoteMapped)
	}
	if j.geo != "" {
		notes = append(notes, geoNote(strings.ToLower(j.geo)))
	}
	if slices.Contains(j.r.Notes, GCDNote) {
		notes = append(notes, GCDNote)
	}
	for _, n := range j.r.Notes {
		if !slices.Contains(notes, n) {
			notes = append(notes, n)
		}
	}
	j.r.Notes = notes
}
