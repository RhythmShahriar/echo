// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"errors"
	"io"
	"time"

	"github.com/RhythmShahriar/echo/consent"
)

// errConsentNotBuilt is what every consent writer returns until it is
// written.
var errConsentNotBuilt = errors.New("report: consent writer not built yet")

// ConsentTextOptions shape the terminal output of the consent commands.
type ConsentTextOptions struct {
	Plain      bool          // --plain: no glyphs, FAIL and ok words
	Color      bool          // color on (a terminal, no --no-color, no NO_COLOR)
	Quiet      bool          // -q: the verdict line and issues only
	Verbose    bool          // -v: the timeline of every run and the issue keys
	Width      int           // terminal width; 0 is 100
	FailOn     string        // --fail-on, for the verdict line
	FirstRun   bool          // the first consent run on this machine: say where the evidence is
	ReportPath string        // the HTML report, for the "full report:" line; "" when none
	BannerWait time.Duration // --banner-wait, for "none found within"; 0 is the 10s default
}

// ConsentMarkdownOptions shape the Markdown summary (--md, the Action
// summary and the PR comment).
type ConsentMarkdownOptions struct {
	Details       bool   // summary-details: issue details and a collapsed timeline per run
	MaxBytes      int    // cut to fit (a PR comment holds 65536); 0 means 900 KiB
	ConsentSHA256 string // the hash of consent.json, quoted in the footer
	FailOn        string // --fail-on, for the verdict word; "" is error
}

// ConsentHTMLOptions shape the HTML evidence report.
type ConsentHTMLOptions struct {
	EvidenceDir   string // the evidence folder screenshots are read from
	ConsentSHA256 string // the hash of consent.json, quoted in the footer
}

// ConsentCatalog is the data of the consent-catalog kind: consent cmps
// fills CMPs, consent trackers fills Trackers.
type ConsentCatalog struct {
	CMPs     []consent.CMPRuleInfo `json:"cmps,omitempty"`
	Trackers []consent.TrackerInfo `json:"trackers,omitempty"`
	Catalog  consent.DataInfo      `json:"catalog"`
	Rules    consent.DataInfo      `json:"rules"`
}

// ConsentVerify is the data of the consent-verify kind: an evidence
// folder checked against its manifest and signature.
type ConsentVerify struct {
	Folder    string          `json:"folder"`
	Files     []VerifyFile    `json:"files"`
	Signature VerifySignature `json:"signature"`
}

// VerifyFile is one file of a verified evidence folder.
type VerifyFile struct {
	Path string `json:"path"`
	OK   bool   `json:"ok"`
	Want string `json:"want"` // the manifest's sha256
	Got  string `json:"got"`  // the file's sha256; "" when it is missing
}

// VerifySignature is the signature check of a verified evidence folder.
type VerifySignature struct {
	Present bool   `json:"present"`
	OK      bool   `json:"ok"`
	Key     string `json:"key"` // the public key file, or its id
}

// WriteConsentHTML writes the HTML evidence report of a consent check.
func WriteConsentHTML(w io.Writer, r *consent.Result, o ConsentHTMLOptions) error {
	return errConsentNotBuilt
}

// WriteConsentJUnit writes a consent check as JUnit XML, one test case
// per page and run; consentSHA256 is quoted in the properties.
func WriteConsentJUnit(w io.Writer, r *consent.Result, consentSHA256 string) error {
	return errConsentNotBuilt
}

// WriteConsentCatalog writes consent cmps (kind "cmps") or consent
// trackers (kind "trackers"), as a table or as a consent-catalog
// envelope.
func WriteConsentCatalog(w io.Writer, cmps []consent.CMPRuleInfo, trackers []consent.TrackerInfo, kind string, asJSON bool, o ConsentTextOptions) error {
	return errConsentNotBuilt
}
