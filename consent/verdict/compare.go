package verdict

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/model"
)

func init() {
	consent.SetComparer(Comparer{})
}

// Comparer is the consent Comparer: it lists what changed from a
// baseline result to the current one (PRD 11.3). Like the Evaluator it
// keeps no state.
type Comparer struct{}

var _ consent.Comparer = Comparer{}

// The tracker part of the change keys that aren't issue keys: a new
// tracker, a new CMP and the consent signals that changed. Issue keys
// start with a check ID (consent/...), so these never collide with one.
const (
	keyTracker      = "tracker"
	keyCMP          = "cmp"
	keyPage         = "page"
	keyDefault      = "consent-mode-default"
	keyGCD          = "gcd"
	keyTCF          = "tcf"
	keyRejectLayer  = "reject-layer"
	pageNotChecked  = "page not checked"
	noValue         = "none"
	maxChangedValue = 400
)

// changeKey is the key of a change that isn't an issue: kind|page|run|id,
// shaped like an issue key so --accept takes both the same way.
func changeKey(kind, page string, run consent.RunKind, id string) string {
	return strings.Join([]string{kind, page, string(run), id}, "|")
}

// pageID names a page across two results: its --sites name and path.
type pageID struct{ site, path string }

func idOf(pg *consent.Page) pageID { return pageID{pg.Site, pagePath(pg.URL)} }

// Compare lists what changed from base to cur. Issues are matched by key
// (and site); a page is matched by its site and path. accept holds the
// change keys taken as intended: they are marked Accepted and don't
// count for --fail-on. A nil cur gives nil; a nil base compares with an
// empty result.
func (Comparer) Compare(base, cur *consent.Result, accept []string) *consent.Comparison {
	if cur == nil {
		return nil
	}
	if base == nil {
		base = &consent.Result{}
	}
	c := &comparison{base: base, cur: cur, jb: newJudge(base, nil), jc: newJudge(cur, nil)}
	c.issues()
	c.pages()
	out := &consent.Comparison{BaseID: base.ID, BaseAt: base.Started, Changes: c.changes}
	accepted := map[string]bool{}
	for _, k := range accept {
		accepted[strings.TrimSpace(k)] = true
	}
	for i := range out.Changes {
		out.Changes[i].Accepted = accepted[out.Changes[i].Key]
	}
	c.sort(out.Changes)
	if out.Changes == nil {
		out.Changes = []consent.Change{}
	}
	return out
}

// comparison is one Compare call: the two results, a judge of each for
// tracker names and the changes found so far.
type comparison struct {
	base, cur *consent.Result
	jb, jc    *judge
	changes   []consent.Change
}

// issueID is an issue as two results match it: its site and key. A key
// from an old file that has none is built from the issue's fields.
type issueID struct{ site, key string }

func idOfIssue(is *consent.Issue) issueID {
	k := is.Key
	if k == "" {
		k = consent.IssueKey(is.Check, is.Page, is.Run, is.Tracker)
	}
	return issueID{is.Site, k}
}

// issues finds broke, new and fixed: the issue keys only one side has.
// An ignored issue in cur is hidden on purpose, so it is never broke or
// new, and it still exists, so the base one isn't fixed either. A base
// issue on a page or run cur didn't check isn't fixed: nothing was
// looked at.
func (c *comparison) issues() {
	inBase := map[issueID]bool{}
	for i := range c.base.Issues {
		inBase[idOfIssue(&c.base.Issues[i])] = true
	}
	inCur := map[issueID]bool{}
	for i := range c.cur.Issues {
		is := &c.cur.Issues[i]
		id := idOfIssue(is)
		inCur[id] = true
		if inBase[id] || is.Ignored != nil {
			continue
		}
		class, title := consent.ClassNew, is.Title
		if is.Level == model.Error {
			class, title = consent.ClassBroke, c.brokeTitle(is)
		}
		c.add(class, id.key, is.Page, is.Run, title, "", string(is.Level))
	}
	for i := range c.base.Issues {
		is := &c.base.Issues[i]
		id := idOfIssue(is)
		if inCur[id] || !c.checked(is.Site, is.Page, is.Run) {
			continue
		}
		c.add(consent.ClassFixed, id.key, is.Page, is.Run, c.fixedTitle(is), string(is.Level), "")
	}
}

// checked reports whether cur has the page and, when run is set, that
// run of it.
func (c *comparison) checked(site, path string, run consent.RunKind) bool {
	pg := findPage(c.cur, pageID{site, path})
	if pg == nil {
		// Issues of no page (an expired ignore for every page) belong to
		// the whole result.
		return path == ""
	}
	return run == "" || runOf(pg, run) != nil
}

// findPage returns r's page with id, or nil.
func findPage(r *consent.Result, id pageID) *consent.Page {
	for i := range r.Pages {
		if idOf(&r.Pages[i]) == id {
			return &r.Pages[i]
		}
	}
	return nil
}

// add appends one change.
func (c *comparison) add(class, key, page string, run consent.RunKind, title, before, after string) {
	c.changes = append(c.changes, consent.Change{Class: class, Key: key, Page: page, Run: run,
		Title: title, Before: capValue(before), After: capValue(after)})
}

// capValue keeps a before or after value to a size a terminal line and
// a PR comment can hold; the full values stay in the two results.
func capValue(s string) string {
	if len(s) <= maxChangedValue {
		return s
	}
	cut := maxChangedValue
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

// utf8Start reports whether b starts a UTF-8 sequence, so a cut never
// splits a character.
func utf8Start(b byte) bool { return b&0xc0 != 0x80 }

// brokeTitle says what an error-level issue new in cur means, with
// "now": "TikTok Pixel now fires before the choice".
func (c *comparison) brokeTitle(is *consent.Issue) string {
	name := c.jc.name(is.Tracker)
	switch is.Check {
	case consent.CheckBeforeChoice:
		return name + " now fires before the choice"
	case consent.CheckAfterReject:
		return name + " now fires after Reject"
	case consent.CheckAfterWithdraw:
		return name + " now fires after consent was withdrawn"
	case consent.CheckScriptBefore:
		return name + " script now loads before the choice"
	case consent.CheckRejectGranted:
		return "Reject is now reported to Google as granted"
	case consent.CheckRejectNotKept:
		return "Reject is no longer kept"
	case consent.CheckRejectBroken:
		return "The Reject button no longer works"
	case consent.CheckNoDefault:
		return "Google tags now run with no Consent Mode default"
	case consent.CheckServerSide:
		return name + " now receives events from the server after Reject"
	}
	return "Now: " + is.Title
}

// fixedTitle says what a base issue gone from cur means: "TikTok Pixel
// no longer fires before the choice".
func (c *comparison) fixedTitle(is *consent.Issue) string {
	name := c.jb.name(is.Tracker)
	switch is.Check {
	case consent.CheckBeforeChoice:
		return name + " no longer fires before the choice"
	case consent.CheckAfterReject:
		return name + " no longer fires after Reject"
	case consent.CheckAfterWithdraw:
		return name + " no longer fires after consent was withdrawn"
	case consent.CheckScriptBefore:
		return name + " script no longer loads before the choice"
	case consent.CheckEmbed:
		return name + " no longer loads before the choice"
	case consent.CheckCookielessPing:
		return name + " no longer sends cookieless pings before the choice"
	case consent.CheckGPCIgnored:
		return name + " no longer fires with Global Privacy Control on"
	case consent.CheckRejectGranted:
		return "Reject is no longer reported to Google as granted"
	case consent.CheckRejectNotKept:
		return "Reject is kept again"
	case consent.CheckRejectBroken:
		return "The Reject button works again"
	case consent.CheckNoDefault:
		return "Google tags now have a Consent Mode default"
	}
	return "Fixed: " + is.Title
}

// pages finds what changed page by page: pages cur no longer checks, new
// trackers and CMPs and the consent signals that differ.
func (c *comparison) pages() {
	for i := range c.base.Pages {
		bp := &c.base.Pages[i]
		if findPage(c.cur, idOf(bp)) == nil {
			path := idOf(bp).path
			c.add(consent.ClassChanged, changeKey(keyPage, path, "", ""), path, "", pageNotChecked, "checked", "not checked")
		}
	}
	for i := range c.cur.Pages {
		cp := &c.cur.Pages[i]
		bp := findPage(c.base, idOf(cp))
		if bp == nil {
			// A page the baseline didn't check has nothing to compare
			// with; its issues are already broke or new.
			continue
		}
		c.cmp(bp, cp)
		c.newTrackers(bp, cp)
		for j := range cp.Runs {
			cr := &cp.Runs[j]
			if br := runOf(bp, cr.Kind); br != nil {
				c.signals(cp, br, cr)
			}
		}
	}
}

// cmp compares the CMP of one page: a CMP where there was none is new,
// another one (or none where there was one) changed.
func (c *comparison) cmp(bp, cp *consent.Page) {
	path := idOf(cp).path
	switch b, n := bp.CMP.ID, cp.CMP.ID; {
	case b == n:
	case n == "":
		c.add(consent.ClassChanged, changeKey(keyCMP, path, "", ""), path, "",
			"The CMP "+cmp.Or(bp.CMP.Name, b)+" is no longer found", b, noValue)
	case b == "":
		name := cmp.Or(cp.CMP.Name, n)
		c.add(consent.ClassNew, changeKey(keyCMP, path, "", n), path, "", name+" banner is new on the page", "", n)
	default:
		c.add(consent.ClassChanged, changeKey(keyCMP, path, "", ""), path, "",
			"The CMP changed from "+cmp.Or(bp.CMP.Name, b)+" to "+cmp.Or(cp.CMP.Name, n), b, n)
	}
}

// presence is where a tracker showed in one run: its first hit's phase,
// or that it only stored cookies or storage keys.
type presence struct {
	phase consent.Phase
	hit   bool
}

// trackersIn lists the trackers of a run with where they showed.
func trackersIn(run *consent.Run) map[string]presence {
	out := map[string]presence{}
	for _, h := range run.Hits {
		if p, ok := out[h.Tracker]; h.Tracker != "" && (!ok || !p.hit) {
			out[h.Tracker] = presence{h.Phase, true}
		}
	}
	for _, ck := range run.Cookies {
		if _, ok := out[ck.Tracker]; ck.Tracker != "" && !ok && ck.Blocked == "" {
			out[ck.Tracker] = presence{ck.Phase, false}
		}
	}
	for _, k := range run.Storage {
		if _, ok := out[k.Tracker]; k.Tracker != "" && !ok {
			out[k.Tracker] = presence{k.Phase, false}
		}
	}
	return out
}

// pageTrackers is every tracker id of any run of pg.
func pageTrackers(pg *consent.Page) map[string]bool {
	out := map[string]bool{}
	for i := range pg.Runs {
		for id := range trackersIn(&pg.Runs[i]) {
			out[id] = true
		}
	}
	return out
}

// newTrackers lists a tracker the baseline's page never had, once per
// run it shows in. A run where a new issue already names the tracker is
// left out: the broke or new issue says it better.
func (c *comparison) newTrackers(bp, cp *consent.Page) {
	path, had := idOf(cp).path, pageTrackers(bp)
	told := map[string]bool{}
	for _, ch := range c.changes {
		if ch.Class != consent.ClassBroke && ch.Class != consent.ClassNew {
			continue
		}
		if parts := strings.Split(ch.Key, "|"); len(parts) == 4 && parts[1] == path {
			told[parts[2]+"|"+parts[3]] = true
		}
	}
	for i := range cp.Runs {
		run := &cp.Runs[i]
		found := trackersIn(run)
		for _, id := range slices.Sorted(maps.Keys(found)) {
			if had[id] || told[string(run.Kind)+"|"+id] {
				continue
			}
			p := found[id]
			verb := " fires "
			if !p.hit {
				verb = " stores cookies "
			}
			c.add(consent.ClassNew, changeKey(keyTracker, path, run.Kind, id), path, run.Kind,
				c.jc.name(id)+verb+phaseWords(run.Kind, p.phase), "", id)
		}
	}
}

// phaseWords says when a tracker showed: "before the choice", "after
// Accept" and so on.
func phaseWords(k consent.RunKind, ph consent.Phase) string {
	switch ph {
	case consent.PhaseAfterAccept:
		if k == consent.RunWithdraw {
			return "after Accept (before the withdraw)"
		}
		return "after Accept"
	case consent.PhaseAfterReject:
		return "after Reject"
	case consent.PhaseAfterWithdraw:
		return "after consent was withdrawn"
	case consent.PhaseGPC:
		return "with Global Privacy Control on"
	}
	return "before the choice"
}

// signals compares the consent signals of one run in both results: the
// Consent Mode default, gcd on the first Google hit, the TCF purposes
// and, for Reject, the layer the button was on.
func (c *comparison) signals(cp *consent.Page, br, cr *consent.Run) {
	path := idOf(cp).path
	if b, n := defaultText(br, c.base.Geo), defaultText(cr, c.cur.Geo); b != n {
		c.add(consent.ClassChanged, changeKey(keyDefault, path, cr.Kind, ""), path, cr.Kind, "The Consent Mode default changed", b, n)
	}
	if b, n := firstGCD(br), firstGCD(cr); b != n && b != "" && n != "" {
		c.add(consent.ClassChanged, changeKey(keyGCD, path, cr.Kind, ""), path, cr.Kind, "gcd on the first Google hit changed", b, n)
	}
	if b, n := tcfPurposes(br), tcfPurposes(cr); b != n && b != "" && n != "" {
		c.add(consent.ClassChanged, changeKey(keyTCF, path, cr.Kind, ""), path, cr.Kind, "The TCF purposes with consent changed", b, n)
	}
	if cr.Kind == consent.RunReject && br.Choice != nil && cr.Choice != nil && br.Choice.Layer != cr.Choice.Layer &&
		br.Choice.Layer > 0 && cr.Choice.Layer > 0 {
		c.add(consent.ClassChanged, changeKey(keyRejectLayer, path, cr.Kind, ""), path, cr.Kind,
			fmt.Sprintf("Reject moved from layer %d to layer %d", br.Choice.Layer, cr.Choice.Layer),
			"layer "+strconv.Itoa(br.Choice.Layer), "layer "+strconv.Itoa(cr.Choice.Layer))
	}
}

// defaultText writes a run's effective Consent Mode default as sorted
// signal=value pairs, or "none".
func defaultText(run *consent.Run, geo string) string {
	state, _, ok := consent.EffectiveDefault(run.Signals, geo)
	if !ok {
		return noValue
	}
	if len(state) == 0 {
		return "empty"
	}
	parts := make([]string, 0, len(state))
	for _, k := range slices.Sorted(maps.Keys(state)) {
		parts = append(parts, k+"="+string(state[k]))
	}
	return strings.Join(parts, " ")
}

// firstGCD is the raw gcd of the run's first Google hit; "" when there
// is no such hit or it carries no gcd (nothing to compare).
func firstGCD(run *consent.Run) string {
	h := firstGoogleHit(run)
	if h == nil || h.Google == nil {
		return ""
	}
	return h.Google.GCD
}

// tcfPurposes writes the purposes the run's last valid TC string grants,
// "none" for none; "" when the run read no TC string.
func tcfPurposes(run *consent.Run) string {
	var last *consent.TCF
	for i := range run.Signals {
		if s := &run.Signals[i]; s.TCF != nil && s.TCF.Valid {
			last = s.TCF
		}
	}
	if last == nil {
		return ""
	}
	if len(last.PurposesConsent) == 0 {
		return noValue
	}
	p := slices.Clone(last.PurposesConsent)
	slices.Sort(p)
	words := make([]string, len(p))
	for i, n := range p {
		words[i] = strconv.Itoa(n)
	}
	return strings.Join(words, ", ")
}

// classRank orders the change classes worst first.
var classRank = map[string]int{consent.ClassBroke: 0, consent.ClassNew: 1, consent.ClassChanged: 2, consent.ClassFixed: 3}

// sort orders changes by class, then page order in cur (pages only the
// baseline had go last), run order (a change of the whole page first)
// and key.
func (c *comparison) sort(changes []consent.Change) {
	pageRank := func(path string) int {
		for i := range c.cur.Pages {
			if pagePath(c.cur.Pages[i].URL) == path {
				return i
			}
		}
		return len(c.cur.Pages)
	}
	slices.SortStableFunc(changes, func(a, b consent.Change) int {
		return cmp.Or(
			cmp.Compare(classRank[a.Class], classRank[b.Class]),
			cmp.Compare(pageRank(a.Page), pageRank(b.Page)),
			cmp.Compare(changeRunRank(a.Run), changeRunRank(b.Run)),
			cmp.Compare(a.Key, b.Key),
		)
	})
}

// changeRunRank is runRank with a page-wide change (no run) first.
func changeRunRank(k consent.RunKind) int {
	if k == "" {
		return -1
	}
	return runRank(k)
}

// MajorVersion is the major version of the results this Echo reads; a
// result from a later major may mean something else by the same field.
const MajorVersion = 1

// ErrNotConsent is what ReadBaseline returns for JSON that is not a
// consent result.
var ErrNotConsent = errors.New("--against: that file isn't a consent result (consent.json or consent --json output)")

// ReadBaseline reads a baseline for --against: a consent.json (the
// Result) from any 1.x Echo, or the consent --json envelope around one.
// Fields this Echo doesn't know are ignored. A result from a newer major
// version is refused with `--against: that result was written by a newer
// Echo (<version>); update Echo` (the CLI adds "rhythm echo: ").
func ReadBaseline(data []byte) (*consent.Result, error) {
	var head struct {
		Schema  string          `json:"schema"`
		Kind    string          `json:"kind"`
		Version string          `json:"version"`
		Data    json.RawMessage `json:"data"`
		Echo    string          `json:"echo"`
		Pages   json.RawMessage `json:"pages"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, ErrNotConsent
	}
	if head.Schema != "" && len(head.Data) > 0 {
		if n, ok := schemaMajor(head.Schema); ok && n > MajorVersion {
			return nil, newerEcho(cmp.Or(head.Version, head.Schema))
		}
		if head.Kind != "consent" {
			return nil, ErrNotConsent
		}
		return ReadBaseline(head.Data)
	}
	if head.Echo == "" && len(head.Pages) == 0 {
		return nil, ErrNotConsent
	}
	if n, ok := versionMajor(head.Echo); ok && n > MajorVersion {
		return nil, newerEcho(head.Echo)
	}
	var r consent.Result
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(&r); err != nil {
		return nil, fmt.Errorf("--against: the result doesn't read as a consent result: %w", err)
	}
	return &r, nil
}

// newerEcho is the error for a result a later major version wrote.
func newerEcho(version string) error {
	return fmt.Errorf("--against: that result was written by a newer Echo (%s); update Echo", version)
}

// schemaMajor reads N from rhythm-echo/vN.
func schemaMajor(s string) (int, bool) {
	rest, ok := strings.CutPrefix(s, "rhythm-echo/v")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	return n, err == nil
}

// versionMajor reads the major of a version like 1.2.0 or v2.0.0-rc.1;
// ok is false for "dev" and other text.
func versionMajor(v string) (int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	major, _, _ := strings.Cut(v, ".")
	n, err := strconv.Atoi(major)
	return n, err == nil
}
