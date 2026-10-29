// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package consent

// EventKind says what an Event records.
type EventKind string

// The event kinds.
const (
	EvRequest  EventKind = "request"
	EvCookie   EventKind = "cookie"
	EvStorage  EventKind = "storage"
	EvSignal   EventKind = "signal"
	EvBanner   EventKind = "banner"
	EvClick    EventKind = "click"
	EvVisit    EventKind = "visit"
	EvDialog   EventKind = "dialog"
	EvPopup    EventKind = "popup"
	EvDownload EventKind = "download"
	EvNote     EventKind = "note"
)

// Event is one thing that happened in a run, in the order it happened.
// A run keeps them in memory for the verdict and writes them to its
// NDJSON file; issues point at them by Seq.
type Event struct {
	Seq     int64       `json:"seq"`
	AtMS    int64       `json:"at_ms"` // ms since the run's first navigation, one clock (PRD 13.3)
	Visit   int         `json:"visit"` // index into Run.Visits
	Phase   Phase       `json:"phase"`
	Run     RunKind     `json:"run,omitempty"` // the run it belongs to, set by the runner
	Kind    EventKind   `json:"kind"`
	Request *Request    `json:"request,omitempty"`
	Hit     *Hit        `json:"hit,omitempty"`
	Cookie  *Cookie     `json:"cookie,omitempty"`
	Storage *StorageKey `json:"storage,omitempty"`
	Signal  *Signal     `json:"signal,omitempty"`
	Note    string      `json:"note,omitempty"` // page-supplied text already passed through SafeText
	// Target is the run's target URL as the plan has it, set by the
	// runner so --live can name the page. It is never written.
	Target string `json:"-"`
}

// Request is a request the page or one of its frames or workers made.
type Request struct {
	Method      string `json:"method"`
	URL         string `json:"url"` // masked (MaskURL), at most 8 KiB
	Host        string `json:"host"`
	Type        string `json:"type"`                // document, script, image, xhr, fetch, beacon, ping, stylesheet, font, media, other
	Frame       string `json:"frame"`               // frame origin; "" for browser traffic
	Initiator   string `json:"initiator,omitempty"` // masked script URL
	Worker      bool   `json:"worker,omitempty"`
	Status      int    `json:"status,omitempty"`
	Held        bool   `json:"held,omitempty"`    // answered in the browser, never sent
	Mapped      bool   `json:"mapped,omitempty"`  // answered by a --map-host address
	Refused     string `json:"refused,omitempty"` // "private address", "browser traffic", "not mapped", "mapped host outside the tab", "limit"
	BodyBytes   int    `json:"body_bytes,omitempty"`
	BodySHA256  string `json:"body_sha256,omitempty"`
	BodyPartial bool   `json:"body_partial,omitempty"`
}

// Hit is a request, cookie or storage key the catalog matched to a
// tracker.
type Hit struct {
	Tracker    string            `json:"tracker"` // catalog id
	Name       string            `json:"name"`
	Category   Category          `json:"category"`
	Need       ConsentNeed       `json:"need"` // not "consent": that key holds Google below (PRD 9.3)
	Kind       HitKind           `json:"kind"`
	ID         string            `json:"id,omitempty"`     // measurement or pixel ID, masked to its last 4
	Events     []string          `json:"events,omitempty"` // page_view, PageView; a GA4 batch gives several (max 256)
	Google     *GoogleConsent    `json:"consent,omitempty"`
	Cookieless bool              `json:"cookieless,omitempty"`
	FirstParty bool              `json:"first_party,omitempty"` // matched by parameters on the site's own host
	Params     map[string]string `json:"params,omitempty"`      // selected, masked parameters the verdict or report needs
}

// GoogleConsent is the consent state a Google hit carries in its
// parameters, raw and decoded.
type GoogleConsent struct {
	GCS           string `json:"gcs,omitempty"`
	GCSDecoded    *GCS   `json:"gcs_decoded,omitempty"`
	GCD           string `json:"gcd,omitempty"`
	GCDDecoded    *GCD   `json:"gcd_decoded,omitempty"`
	GCDUnofficial bool   `json:"gcd_unofficial,omitempty"` // always true when GCDDecoded is set
	NPA           string `json:"npa,omitempty"`
	DMA           string `json:"dma,omitempty"`
}

// GCS is a decoded gcs parameter (G100, G111...): the two storage
// signals.
type GCS struct {
	Known            bool  `json:"known"`
	AdStorage        Value `json:"ad_storage"`
	AnalyticsStorage Value `json:"analytics_storage"`
}

// GCDSignal is one signal of a decoded gcd parameter: its letter and
// what the letter says about the default and the update.
type GCDSignal struct {
	Letter  string `json:"letter"`
	Default Value  `json:"default"`
	Update  Value  `json:"update"`
	Known   bool   `json:"known"`
}

// GCD is a decoded gcd parameter (13p3p3p3p5). Google doesn't document
// it, so the reading is the community's and every output says so.
type GCD struct {
	Known             bool      `json:"known"`
	Extra             string    `json:"extra,omitempty"` // trailing part after the four signals, kept and ignored
	Note              string    `json:"note,omitempty"`  // e.g. "letter e is documented by one source"
	AdStorage         GCDSignal `json:"ad_storage"`
	AnalyticsStorage  GCDSignal `json:"analytics_storage"`
	AdUserData        GCDSignal `json:"ad_user_data"`
	AdPersonalization GCDSignal `json:"ad_personalization"`
}

// Signal is a consent signal the page sent: a Consent Mode command, a
// Meta or Microsoft consent call, a TCF or GPP string or a CMP's state.
type Signal struct {
	Source          string           `json:"source"`          // gtag, datalayer, fbq, uetq, tcf, gpp, cmp
	Command         string           `json:"command"`         // default, update, revoke, grant, init, set, tcloaded, useractioncomplete
	AtMS            int64            `json:"at_ms"`           // run clock; 0 from the decoder, filled by browse
	State           map[string]Value `json:"state,omitempty"` // ad_storage, analytics_storage, ad_user_data, ad_personalization, functionality_storage, personalization_storage, security_storage
	Region          []string         `json:"region,omitempty"`
	WaitForUpdateMS int              `json:"wait_for_update_ms,omitempty"`
	TCF             *TCF             `json:"tcf,omitempty"`
	GPP             *GPP             `json:"gpp,omitempty"`
	Raw             string           `json:"raw,omitempty"` // capped 4 KiB, masked
}

// TCF is a decoded IAB TCF consent string.
type TCF struct {
	Version         int   `json:"version"`
	CMPID           int   `json:"cmp_id"`
	PurposesConsent []int `json:"purposes_consent"`
	PurposesLI      []int `json:"purposes_li,omitempty"`
	VendorsConsent  int   `json:"vendors_consent"` // count
	Valid           bool  `json:"valid"`
}

// GPP is a decoded IAB GPP string: its sections and the US opt-outs.
type GPP struct {
	Sections    []string `json:"sections"`
	SaleOptOut  Value    `json:"sale_opt_out,omitempty"`
	ShareOptOut Value    `json:"sharing_opt_out,omitempty"`
	Valid       bool     `json:"valid"`
}

// Cookie is a cookie the browser stored, or refused to. Its value is
// never kept, only its length.
type Cookie struct {
	Name     string      `json:"name"`
	Domain   string      `json:"domain"`
	Path     string      `json:"path"`
	Expires  int64       `json:"expires,omitempty"` // unix seconds; 0 session
	HTTPOnly bool        `json:"http_only,omitempty"`
	Secure   bool        `json:"secure,omitempty"`
	SameSite string      `json:"same_site,omitempty"`
	Length   int         `json:"length"` // the value is never stored
	Tracker  string      `json:"tracker,omitempty"`
	Category Category    `json:"category,omitempty"`
	Need     ConsentNeed `json:"need,omitempty"`
	Phase    Phase       `json:"phase"`
	AtMS     int64       `json:"at_ms"`
	SetBy    string      `json:"set_by,omitempty"`  // "script", "response:<host>", or SetByEcho
	Blocked  string      `json:"blocked,omitempty"` // Chrome's blocked reason when it refused to store it
}

// SetByEcho is the SetBy of a cookie Echo itself added with --cookie
// (a site's own in --sites too) whose value is still the one Echo set.
// The page's scripts didn't write it. The verdict never counts it.
const SetByEcho = "echo"

// StorageKey is a key a page wrote to local or session storage or
// IndexedDB. Its value is never kept, only its length.
type StorageKey struct {
	Area     string      `json:"area"` // local, session, indexeddb
	Origin   string      `json:"origin"`
	Key      string      `json:"key"`
	Length   int         `json:"length"`
	Tracker  string      `json:"tracker,omitempty"`
	Category Category    `json:"category,omitempty"`
	Need     ConsentNeed `json:"need,omitempty"`
	Phase    Phase       `json:"phase"`
	AtMS     int64       `json:"at_ms"`
}

// RunNote is something the page did that the run handled and a reader
// should hear about: a dialog, a popup or a download (PRD 14.10).
type RunNote struct {
	AtMS int64  `json:"at_ms"`
	Kind string `json:"kind"` // dialog, popup, download or visit
	Text string `json:"text"` // safe to print
}

// MaxRunNotes caps the notes one run keeps; a page that opens a dialog
// on every frame says so once.
const MaxRunNotes = 20

// EventNote is the run note e makes, or "" for an event that makes
// none: `the page opened a dialog ("Allow?"); Echo dismissed it`, `the
// page opened a popup (https://ads.example/); Echo closed it`, `the page
// started a download (https://shop.test/file.bin); Chrome refused it`.
// The event's note is already masked and passed through SafeText.
func EventNote(e Event) string {
	what := func(s, quoted string) string {
		if quoted == "" {
			return s
		}
		return s + " (" + quoted + ")"
	}
	switch e.Kind {
	case EvDialog:
		q := ""
		if e.Note != "" {
			q = `"` + SafeText(e.Note, 200) + `"`
		}
		return what("the page opened a dialog", q) + "; Echo dismissed it"
	case EvPopup:
		return what("the page opened a popup", SafeText(e.Note, 200)) + "; Echo closed it"
	case EvDownload:
		return what("the page started a download", SafeText(e.Note, 200)) + "; Chrome refused it"
	}
	return ""
}

// AddNote adds the run note e makes, once per text and at most
// MaxRunNotes.
func (r *Run) AddNote(e Event) {
	r.addNote(RunNote{AtMS: e.AtMS, Kind: string(e.Kind), Text: EventNote(e)})
}

// NoteVisit is the kind of the run note a later page or a reload that
// didn't load makes.
const NoteVisit = "visit"

// AddVisitNote notes that v, a later page or the reload, didn't load:
// `the reload (https://shop.test/) answered 500; the choice wasn't
// checked there`. v.URL is masked already.
func (r *Run) AddVisitNote(v Visit) {
	if v.Error == "" {
		return
	}
	what := "the page " + SafeText(v.URL, 200)
	if v.Step == "reload" {
		what = "the reload (" + SafeText(v.URL, 200) + ")"
	}
	r.addNote(RunNote{AtMS: v.AtMS, Kind: NoteVisit, Text: what + " " + SafeText(v.Error, 200) + "; the choice wasn't checked there"})
}

// addNote adds n once per text and at most MaxRunNotes.
func (r *Run) addNote(n RunNote) {
	if n.Text == "" || len(r.Notes) >= MaxRunNotes {
		return
	}
	for _, o := range r.Notes {
		if o.Text == n.Text {
			return
		}
	}
	r.Notes = append(r.Notes, n)
}
