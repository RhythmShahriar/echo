// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package consent

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/model"
)

// BrowserOptions choose the Chrome a check runs in.
type BrowserOptions struct {
	Bin        string    // --browser; "" finds one or downloads
	NoDownload bool      // --no-download
	NoSandbox  bool      // --no-sandbox
	Visible    bool      // --show-browser
	Log        io.Writer // "Browser: ..." and download lines
}

// Limits bound what one check records, so a hostile or broken page can't
// fill the disk or the memory (PRD 13.8).
type Limits struct {
	Requests    int // 20000 per run
	URLBytes    int // 8192: longer URLs are cut
	BodyBytes   int // 65536: the parse window of a request body
	BatchEvents int // 256 events from one GA4 batch
	HookRecords int // 2000 hook records per run
	HookBytes   int // 2 MiB of hook records per run
	Screenshots int // 4 per run
	Pages       int // 10 extra pages per run
	TotalPages  int // 50 per command
	Channel     int // 4096 buffered events between the browser and the recorder
}

// DefaultLimits are the limits a check uses unless a test sets others.
var DefaultLimits = Limits{20000, 8192, 65536, 256, 2000, 2 << 20, 4, 10, 50, 4096}

// Target is one page to check.
type Target struct {
	Site string   // --sites name
	URL  *url.URL // absolute http or https
	Args []string // per-site flags already applied by the CLI; kept for the record
}

// EchoLink is the Echo API --echo asks what the site sent server-side.
type EchoLink struct{ URL, Token string }

// Plan is everything a check needs, resolved from flags, the config and
// --sites by the CLI.
type Plan struct {
	ID                              string // the result folder name
	Dir                             string // evidence folder (absolute), created by the CLI
	Targets                         []Target
	Runs                            []RunKind
	Pages                           int
	Settle                          time.Duration
	BannerWait                      time.Duration
	Timeout                         time.Duration
	Retries                         int
	Parallel                        int
	CMP                             string // "auto" or a rule id
	Click                           ClickMode
	AcceptSelector                  []Locator
	RejectSelector                  []Locator
	Policy                          Policy
	Exempt                          []string
	Hold                            bool // true unless --send-hits
	MapHosts                        map[string]string
	MapOnly                         bool
	Geo, Locale, Timezone, Viewport string
	UserAgent                       string            // "" means the default rewrite plus the suffix
	Headers                         map[string]string // already resolved from @file/env:NAME
	Cookies                         []string
	Proxy                           string
	Insecure                        bool
	InsecurePublic                  bool
	FollowHost                      bool
	Browser                         BrowserOptions
	Echo                            *EchoLink
	Screenshots                     bool
	HAR                             bool
	ShowIDs                         bool
	Ignore                          []Ignore
	ExtraTrackers                   []byte                 // YAML of consent.trackers from the config, validated by the CLI
	CheckLevels                     map[string]model.Level // from checks: (consent/ IDs only; "off" removes)
	Limits                          Limits
	Version                         string      // Echo's version, for the UA suffix and the report
	Live                            func(Event) // --live; nil otherwise
}

// The ranges Validate holds a plan to (PRD 7). The messages spell the
// durations out as the help does (60s, not 1m).
const (
	MaxPages      = 10
	MaxSettle     = 30 * time.Second
	MaxBannerWait = 60 * time.Second
	MaxTimeout    = 10 * time.Minute
	MaxRetries    = 3
	MaxParallel   = 8
)

// Validate checks ranges (PRD 7) and returns usage errors with the exact
// texts. Range messages follow one pattern: `--pages 11: want 0 to 10`,
// `--settle 45s: want at most 30s`, `--retries 5: want 0 to 3`,
// `--parallel 9: want 1 to 8`, `--banner-wait 2m: want at most 60s`,
// `--timeout 11m: want at most 10m`. It also checks the run kinds, the
// policy, the click mode and that every target is an http or https URL.
func (p *Plan) Validate() error {
	switch {
	case p.Pages < 0 || p.Pages > MaxPages:
		return fmt.Errorf("--pages %d: want 0 to %d", p.Pages, MaxPages)
	case p.Settle < 0:
		return fmt.Errorf("--settle %s: want 0 to 30s", fmtDuration(p.Settle))
	case p.Settle > MaxSettle:
		return fmt.Errorf("--settle %s: want at most 30s", fmtDuration(p.Settle))
	case p.BannerWait < 0:
		return fmt.Errorf("--banner-wait %s: want 0 to 60s", fmtDuration(p.BannerWait))
	case p.BannerWait > MaxBannerWait:
		return fmt.Errorf("--banner-wait %s: want at most 60s", fmtDuration(p.BannerWait))
	case p.Timeout <= 0:
		return fmt.Errorf("--timeout %s: want more than 0 and at most 10m", fmtDuration(p.Timeout))
	case p.Timeout > MaxTimeout:
		return fmt.Errorf("--timeout %s: want at most 10m", fmtDuration(p.Timeout))
	case p.Retries < 0 || p.Retries > MaxRetries:
		return fmt.Errorf("--retries %d: want 0 to %d", p.Retries, MaxRetries)
	case p.Parallel < 1 || p.Parallel > MaxParallel:
		return fmt.Errorf("--parallel %d: want 1 to %d", p.Parallel, MaxParallel)
	case len(p.Runs) == 0:
		return errors.New("--runs: want at least one of none, accept, reject, gpc or withdraw")
	}
	for _, k := range p.Runs {
		if !slices.Contains(AllRuns, k) {
			return fmt.Errorf("--runs %q: want none, accept, reject, gpc or withdraw (+gpc adds one)", k)
		}
	}
	if _, err := ParsePolicy(string(p.Policy)); err != nil {
		return err
	}
	if _, err := ParseClickMode(string(p.Click)); err != nil {
		return err
	}
	for _, t := range p.Targets {
		if t.URL == nil || (t.URL.Scheme != "http" && t.URL.Scheme != "https") || t.URL.Host == "" {
			shown := ""
			if t.URL != nil {
				shown = MaskURL(t.URL.String(), false)
			}
			return fmt.Errorf("consent %q: want an http or https URL", shown)
		}
	}
	return nil
}

// fmtDuration writes d the way people type it: 2m, not 2m0s.
func fmtDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// EffectiveDefault returns the Consent Mode default that applies to region
// (ISO 3166-1 alpha-2, "" for none) and its AtMS: among default Signals
// (Source gtag or datalayer, Command default), a region-scoped one whose
// Region contains region wins over a global one; within the same scope the
// last by AtMS wins (Google's documented rule). ok is false when none applies.
// Shared by signal tests and the verdict lane, so the rule lives here.
func EffectiveDefault(signals []Signal, region string) (state map[string]Value, atMS int64, ok bool) {
	var global, scoped *Signal
	later := func(cur *Signal, s *Signal) bool { return cur == nil || s.AtMS >= cur.AtMS }
	for i := range signals {
		s := &signals[i]
		if (s.Source != "gtag" && s.Source != "datalayer") || s.Command != "default" {
			continue
		}
		switch {
		case len(s.Region) == 0:
			if later(global, s) {
				global = s
			}
		case region != "" && slices.ContainsFunc(s.Region, func(r string) bool { return strings.EqualFold(r, region) }):
			if later(scoped, s) {
				scoped = s
			}
		}
	}
	pick := scoped
	if pick == nil {
		pick = global
	}
	if pick == nil {
		return nil, 0, false
	}
	state = make(map[string]Value, len(pick.State))
	for k, v := range pick.State {
		state[k] = v
	}
	return state, pick.AtMS, true
}
