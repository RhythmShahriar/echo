// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package consent

import (
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/model"
)

// Visit is one page load in a run: the first load, a later page or the
// reload.
type Visit struct {
	URL    string `json:"url"`  // masked
	Step   string `json:"step"` // load, page, reload
	AtMS   int64  `json:"at_ms"`
	Banner bool   `json:"banner"` // the banner was showing after this visit settled
}

// CMPState is a CMP's stored choice, as its cookie, storage or API says.
type CMPState struct {
	CMP        string          `json:"cmp"`
	Source     string          `json:"source"` // "cookie:CookieConsent", "api:Cookiebot.consent", "tcf", "banner"
	Decided    bool            `json:"decided"`
	Categories map[string]bool `json:"categories,omitempty"` // statistics, marketing, preferences...
	TCF        *TCF            `json:"tcf,omitempty"`
}

// CMPInfo is the consent management platform found on a page.
type CMPInfo struct {
	ID          string `json:"id"` // rule id, "" when none
	Name        string `json:"name"`
	Found       bool   `json:"found"`       // a banner was shown
	Present     bool   `json:"present"`     // the CMP's script or global exists, shown or not
	DetectedBy  string `json:"detected_by"` // "selector:#CybotCookiebotDialog", "global:Cookiebot", "script:consent.cookiebot.com", "generic"
	BannerAtMS  int64  `json:"banner_at_ms"`
	TCF         bool   `json:"tcf"`
	RejectLayer int    `json:"reject_layer"` // 1, 2; 0 unknown
}

// Locator finds one element: CSS inside the innermost shadow root or
// frame named by Shadow and Frame.
type Locator struct {
	CSS    string   `json:"css"`              // inside the innermost shadow root or frame
	Shadow []string `json:"shadow,omitempty"` // host selectors from the document down
	Frame  string   `json:"frame,omitempty"`  // frame URL substring; "" the top document
}

// Box is an element's place on the page in CSS pixels, for the outline
// on the screenshot. Results written before 1.2.0 shipped used X, Y, W
// and H; encoding/json reads those keys too (it matches any case).
type Box struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// Choice is how a run made its choice (Accept or Reject) and whether the
// CMP confirmed it.
type Choice struct {
	How        string    `json:"how"`      // button, api
	Selector   string    `json:"selector"` // masked, capped 200
	Text       string    `json:"text"`     // safe text, capped 120
	Layer      int       `json:"layer"`
	Clicks     int       `json:"clicks"`
	AtMS       int64     `json:"at_ms"`
	Box        *Box      `json:"box,omitempty"` // for the screenshot outline
	Verified   bool      `json:"verified"`
	VerifiedBy string    `json:"verified_by"` // "cookie:<name>", "api", "tcf", "banner"
	State      *CMPState `json:"state,omitempty"`
	Fallback   bool      `json:"fallback"`
	APIWorked  bool      `json:"api_worked,omitempty"` // set when the button failed and the API reject was tried
}

// Script is a tracker's script the page loaded.
type Script struct {
	Tracker string `json:"tracker"`
	URL     string `json:"url"`
	AtMS    int64  `json:"at_ms"`
	Phase   Phase  `json:"phase"`
	Seq     int64  `json:"seq"`
}

// HitRef is a hit as listed in a run: the hit with where and when it
// happened.
type HitRef struct {
	Seq   int64  `json:"seq"`
	AtMS  int64  `json:"at_ms"`
	Phase Phase  `json:"phase"`
	Visit int    `json:"visit"`
	URL   string `json:"url"`
	Held  bool   `json:"held"`
	Hit
}

// EchoMessage is a server-side message Echo caught during a run (--echo).
type EchoMessage struct {
	ID       string `json:"id"`
	Provider string `json:"provider"` // ga4-mp, meta-capi
	Event    string `json:"event"`
	AtMS     int64  `json:"at_ms"`
	Phase    Phase  `json:"phase"`
}

// Run is one run of a page: what was loaded, chosen and recorded.
type Run struct {
	Kind        RunKind           `json:"run"`
	Status      RunStatus         `json:"status"`
	Reason      Reason            `json:"reason"`
	Detail      string            `json:"detail,omitempty"`
	Attempts    int               `json:"attempts"`
	DurationMS  int64             `json:"duration_ms"`
	Choice      *Choice           `json:"choice"`
	Withdraw    *Choice           `json:"withdraw,omitempty"`
	Visits      []Visit           `json:"visits"`
	Hits        []HitRef          `json:"hits"`
	Cookies     []Cookie          `json:"cookies"`
	Storage     []StorageKey      `json:"storage"`
	Scripts     []Script          `json:"scripts"`
	Signals     []Signal          `json:"signals"`               // each with its AtMS
	FinalState  *CMPState         `json:"final_state,omitempty"` // CMPDriver.State after the reload (accept, reject, withdraw)
	Server      []EchoMessage     `json:"server,omitempty"`
	Screenshots map[string]string `json:"screenshots"` // "banner", "after" -> path relative to the evidence folder
	EventsFile  string            `json:"events_file"`
	HARFile     string            `json:"har_file,omitempty"`
	Truncated   bool              `json:"truncated"`
	Dropped     int               `json:"dropped"`
	Notes       []RunNote         `json:"notes,omitempty"` // dialogs, popups and downloads the page made (PRD 14.10)
	Events      []Event           `json:"-"`               // in memory for the verdict; on disk as NDJSON
}

// Inconclusive reports whether the run couldn't show what it was for:
// it has a Reason.
func (r Run) Inconclusive() bool { return r.Reason != ReasonNone }

// ReopenFailed reports whether a withdraw run stopped because the banner
// couldn't be reopened after Accept: its reason is no_button, Accept
// was chosen, nothing was withdrawn and Detail holds why the reopen
// failed (a missing Accept button leaves Detail empty).
func (r Run) ReopenFailed() bool {
	return r.Kind == RunWithdraw && r.Reason == ReasonNoButton && r.Choice != nil && r.Withdraw == nil && r.Detail != ""
}

// ConsentMode is what a page's Google Consent Mode setup looked like in
// the none run.
type ConsentMode struct {
	Present               bool             `json:"present"`
	V2                    bool             `json:"v2"`
	DefaultAtMS           int64            `json:"default_at_ms"`
	FirstGoogleHitMS      int64            `json:"first_google_hit_ms"`
	DefaultBeforeFirstHit bool             `json:"default_before_first_hit"`
	Default               map[string]Value `json:"default,omitempty"`
	WaitForUpdateMS       int              `json:"wait_for_update_ms,omitempty"`
	Region                []string         `json:"region,omitempty"`
}

// Page is one checked URL with its runs.
type Page struct {
	Site        string       `json:"site"` // --sites name, "" otherwise
	URL         string       `json:"url"`
	FinalURL    string       `json:"final_url"`
	Redirects   []string     `json:"redirects"`
	CMP         CMPInfo      `json:"cmp"`
	ConsentMode *ConsentMode `json:"consent_mode"`     // filled by the verdict stage
	NeedsNone   bool         `json:"needs_no_consent"` // PRD 11.2, filled by the verdict stage
	Runs        []Run        `json:"runs"`
}

// Issue is one finding of a consent check.
type Issue struct {
	Check    string      `json:"check"`
	Level    model.Level `json:"level"`
	Key      string      `json:"key"`  // check|page|run|tracker, page as site:page with --sites
	Page     string      `json:"page"` // path of the page (host and path when the pages span hosts), or the URL when off-site
	Site     string      `json:"site,omitempty"`
	Run      RunKind     `json:"run"`
	Tracker  string      `json:"tracker,omitempty"`
	Policy   Policy      `json:"policy"`
	Title    string      `json:"title"`
	Detail   string      `json:"detail"`
	Fix      string      `json:"fix"`
	Evidence []int64     `json:"evidence"`
	Ignored  *Ignore     `json:"ignored,omitempty"`
}

// IssueKey is an issue's stable key, check|page|run|tracker, which
// comparisons, --accept and ignore: entries use. It stays the same
// between releases.
func IssueKey(check, page string, run RunKind, tracker string) string {
	return strings.Join([]string{check, page, string(run), tracker}, "|")
}

// Ignore is an ignore: entry of the config: a known issue hidden until
// a date, with the reason.
type Ignore struct {
	Check   string  `json:"check" yaml:"check"`
	Tracker string  `json:"tracker,omitempty" yaml:"tracker,omitempty"`
	Page    string  `json:"page,omitempty" yaml:"page,omitempty"`
	Run     RunKind `json:"run,omitempty" yaml:"run,omitempty"`
	Reason  string  `json:"reason" yaml:"reason"`
	Until   string  `json:"until" yaml:"until"` // YYYY-MM-DD
}

// Inconclusive is a run that couldn't show what it was for, as the
// result lists it.
type Inconclusive struct {
	Page   string  `json:"page"`
	Site   string  `json:"site,omitempty"` // --sites name, "" otherwise
	Run    RunKind `json:"run"`
	Reason Reason  `json:"reason"`
	Detail string  `json:"detail"`
}

// The classes of a Change, worst first.
const (
	ClassBroke   = "broke"
	ClassNew     = "new"
	ClassChanged = "changed"
	ClassFixed   = "fixed"
)

// Change is one difference from a baseline (--against).
type Change struct {
	Class    string  `json:"class"` // broke, new, changed, fixed
	Key      string  `json:"key"`
	Site     string  `json:"site,omitempty"` // --sites name, "" otherwise
	Page     string  `json:"page"`
	Run      RunKind `json:"run"`
	Title    string  `json:"title"`
	Before   string  `json:"before,omitempty"`
	After    string  `json:"after,omitempty"`
	Accepted bool    `json:"accepted,omitempty"`
}

// Comparison is a result compared with a baseline.
type Comparison struct {
	Against string    `json:"against"` // name or path
	BaseID  string    `json:"base_id"`
	BaseAt  time.Time `json:"base_at"`
	Changes []Change  `json:"changes"`
}

// EvidenceFile is one file of the evidence folder with its hash.
type EvidenceFile struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// Evidence is the result's evidence folder and every file in it.
type Evidence struct {
	Folder string         `json:"folder"`
	Files  []EvidenceFile `json:"files"`
}

// DataInfo names a compiled-in data set: the tracker catalog or the
// CMP rules.
type DataInfo struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
	Count   int    `json:"count"`
}

// Network counts what the browser asked for across the whole check.
type Network struct {
	Requests        int `json:"requests"`
	Held            int `json:"held"`
	Refused         int `json:"refused"`
	BrowserInternal int `json:"browser_internal"`
}

// Result is a whole consent check: the setup, every page with its runs
// and the verdict. It is the data of the consent envelope and of
// consent.json.
type Result struct {
	ID           string         `json:"id"`
	Started      time.Time      `json:"started"`
	DurationMS   int64          `json:"duration_ms"`
	Echo         string         `json:"echo"`
	Browser      string         `json:"browser"`
	UserAgent    string         `json:"user_agent"`
	OS           string         `json:"os"`
	Sandbox      bool           `json:"sandbox"`
	TLSVerified  bool           `json:"tls_verified"`
	Held         bool           `json:"held"`
	Mapped       bool           `json:"mapped"`
	Policy       Policy         `json:"policy"`
	Geo          string         `json:"geo"`
	Locale       string         `json:"locale"`
	Timezone     string         `json:"timezone"`
	Viewport     string         `json:"viewport"`
	Catalog      DataInfo       `json:"catalog"`
	CMPRules     DataInfo       `json:"cmp_rules"`
	Network      Network        `json:"network"`
	Pages        []Page         `json:"pages"`
	Issues       []Issue        `json:"issues"`
	Inconclusive []Inconclusive `json:"inconclusive"`
	Comparison   *Comparison    `json:"comparison"`
	Evidence     Evidence       `json:"evidence"`
	Notes        []string       `json:"notes"`
	Complete     bool           `json:"complete"` // false when a run was inconclusive, cut short or canceled; consent.json says it too
	// ShowIDs is set when the check ran with --show-ids: its URLs, cookie
	// names and details keep tracking IDs unmasked, so consent report
	// refuses Markdown and JUnit for it as the check did.
	ShowIDs bool `json:"show_ids,omitempty"`
}

// Summary is the envelope summary (PRD 9.3), computed from a Result.
type Summary struct {
	Broke, New, Changed, Fixed                int // changes by class, with --against; accepted ones don't count
	Errors, Warnings, Notices                 int // issues by level; ignored ones don't count
	Pages, Runs, FailedRuns, InconclusiveRuns int
	Trackers, BeforeChoice, AfterReject, Held int
}

// Summary counts r for the envelope. FailedRuns are runs that ended
// other than ok or no-banner; InconclusiveRuns those with a Reason that
// counts (incomplete). Trackers is the number of different trackers seen
// in any hit, cookie or storage key; BeforeChoice and AfterReject the
// number of different consent-needing trackers that sent a hit before
// the choice or after Reject (cookieless pings under advanced don't
// count). Held is Network.Held.
func (r *Result) Summary() Summary {
	var s Summary
	if c := r.Comparison; c != nil {
		for _, ch := range c.Changes {
			if ch.Accepted {
				continue
			}
			switch ch.Class {
			case ClassBroke:
				s.Broke++
			case ClassNew:
				s.New++
			case ClassChanged:
				s.Changed++
			case ClassFixed:
				s.Fixed++
			}
		}
	}
	for _, is := range r.Issues {
		if is.Ignored != nil {
			continue
		}
		switch is.Level {
		case model.Error:
			s.Errors++
		case model.Warning:
			s.Warnings++
		case model.Notice:
			s.Notices++
		}
	}
	trackers := map[string]bool{}
	before, afterReject := map[string]bool{}, map[string]bool{}
	s.Pages = len(r.Pages)
	for _, p := range r.Pages {
		for _, run := range p.Runs {
			s.Runs++
			if run.Status != StatusOK && run.Status != StatusNoBanner {
				s.FailedRuns++
			}
			if runIncomplete(p, run) {
				s.InconclusiveRuns++
			}
			for _, h := range run.Hits {
				trackers[h.Tracker] = true
				if h.Need != NeedNeeded || h.Kind == HitScript || (h.Cookieless && r.Policy == PolicyAdvanced) {
					continue
				}
				switch h.Phase {
				case PhaseBefore:
					before[h.Tracker] = true
				case PhaseAfterReject:
					afterReject[h.Tracker] = true
				}
			}
			for _, c := range run.Cookies {
				if c.Tracker != "" {
					trackers[c.Tracker] = true
				}
			}
			for _, k := range run.Storage {
				if k.Tracker != "" {
					trackers[k.Tracker] = true
				}
			}
		}
	}
	delete(trackers, "")
	s.Trackers, s.BeforeChoice, s.AfterReject = len(trackers), len(before), len(afterReject)
	s.Held = r.Network.Held
	return s
}

// runIncomplete reports whether run leaves the result incomplete: it is
// inconclusive, cut short by a limit or canceled. A no-banner run on a
// page that needs no consent is complete (PRD 11.2).
func runIncomplete(p Page, run Run) bool {
	if p.NeedsNone && run.Status == StatusNoBanner && (run.Reason == ReasonNone || run.Reason == ReasonNoBanner) {
		return false
	}
	return run.Inconclusive() || run.Truncated || run.Status == StatusCanceled
}

// incomplete reports whether any part of r couldn't be checked.
func (r *Result) incomplete() bool {
	if len(r.Inconclusive) > 0 {
		return true
	}
	for _, p := range r.Pages {
		for _, run := range p.Runs {
			if runIncomplete(p, run) {
				return true
			}
		}
	}
	return false
}

// failLevel reads --fail-on as the words Verdict and ExitCode use: an
// issue level, a change class or none. "" is error, or broke when
// comparing.
func failLevel(failOn string, comparing bool) string {
	f := strings.ToLower(strings.TrimSpace(failOn))
	if f == "" {
		if comparing {
			return ClassBroke
		}
		return string(model.Error)
	}
	return f
}

// changeRank orders the change classes that can fail a build, worst
// first; fixed never does.
var changeRank = map[string]int{ClassBroke: 0, ClassNew: 1, ClassChanged: 2}

// failing reports whether r has an issue or change at or above failOn.
func (r *Result) failing(failOn string) bool {
	f := failLevel(failOn, r.Comparison != nil)
	if f == "none" {
		return false
	}
	if want, ok := changeRank[f]; ok {
		if r.Comparison == nil {
			return false
		}
		for _, c := range r.Comparison.Changes {
			if rank, ok := changeRank[c.Class]; ok && !c.Accepted && rank <= want {
				return true
			}
		}
		return false
	}
	want := model.Level(f)
	for _, is := range r.Issues {
		if is.Ignored == nil && is.Level.Rank() <= want.Rank() {
			return true
		}
	}
	return false
}

// Verdict is "ok", "findings" or "error" for the envelope, given failOn.
// A result with an inconclusive run is "findings" when something is at
// or above failOn and "error" otherwise; a complete one is "findings"
// when it has any issue or change that counts, else "ok".
func (r *Result) Verdict(failOn string) string {
	if r.incomplete() {
		if r.failing(failOn) {
			return "findings"
		}
		return "error"
	}
	s := r.Summary()
	if s.Broke+s.New+s.Changed+s.Errors+s.Warnings+s.Notices > 0 {
		return "findings"
	}
	return "ok"
}

// Head words for a result that isn't a comparison.
const (
	HeadFindings     = "Findings"
	HeadInconclusive = "Couldn't check"
	HeadClear        = "All clear"
)

// Head is the verdict word the reports print. It comes from what was
// found, not from failOn, so --fail-on none never turns errors into
// "All clear": Findings when there is an error or warning (or anything
// failOn counts), Couldn't check when a run was inconclusive, else All
// clear. failOn decides only the exit code.
func (r *Result) Head(failOn string) string {
	s := r.Summary()
	switch {
	case s.Errors+s.Warnings > 0 || r.failing(failOn):
		return HeadFindings
	case r.incomplete():
		return HeadInconclusive
	}
	return HeadClear
}

// ExitCode applies PRD 10.1 precedence: 1 when an issue (or, comparing,
// a change) is at or above failOn, else 3 when any run is inconclusive,
// else 0. Interrupts (130) and usage errors (2) are the CLI's.
func (r *Result) ExitCode(failOn string) int {
	switch {
	case r.failing(failOn):
		return 1
	case r.incomplete():
		return 3
	}
	return 0
}
