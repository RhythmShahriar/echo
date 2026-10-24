// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"regexp"
	"strings"

	"github.com/RhythmShahriar/echo/check"
	"github.com/RhythmShahriar/echo/compare"
	"github.com/RhythmShahriar/echo/model"
)

//go:embed report.html.tmpl
var htmlSource string

var htmlTemplate = template.Must(template.New("report").Parse(htmlSource))

// Email previews start with emailBlocked, which keeps remote images (and
// with them tracking pixels) from loading when someone opens the report.
// The "Load remote images" button swaps it for emailAllowed; the report's
// script holds the same two strings.
const (
	emailBlocked = "default-src 'none'; img-src data:; style-src 'unsafe-inline'"
	emailAllowed = "default-src 'none'; img-src data: https: http:; style-src 'unsafe-inline'"
)

// reportCSP is the report's own policy: nothing remote, inline styles, and
// only the one inline script, by hash. An email preview is a srcdoc iframe,
// which inherits this policy and adds emailBlocked on top. Policies only
// ever narrow, so the page has to allow remote images for the button to be
// able to: the page itself has none, since every value in it is escaped.
// Remote CSS and fonts stay blocked even then.
var reportCSP = "default-src 'none'; style-src 'unsafe-inline'; img-src data: https: http:; frame-src 'self'; " +
	"base-uri 'none'; form-action 'none'; script-src '" + scriptHash(htmlSource) + "'"

// scriptHash is the CSP source for the template's one inline script.
func scriptHash(src string) string {
	_, rest, _ := strings.Cut(src, "<script>")
	body, _, _ := strings.Cut(rest, "</script>")
	sum := sha256.Sum256([]byte(body))
	return "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
}

// remoteRef finds a reference to a remote resource in email HTML: an
// image, a background or a url() in CSS.
var remoteRef = regexp.MustCompile(`(?i)(?:src|srcset|background)\s*=\s*["']?\s*(?:https?:)?//|url\(\s*["']?\s*(?:https?:)?//`)

type htmlData struct {
	CSP                                          string
	Title, Version, Target, Created, Mode, Tally string
	Compared                                     bool
	Verdict, Tone, Against                       string
	Chips                                        []htmlChip
	Changes                                      []htmlItem
	Issues                                       []htmlItem
	Actions                                      []htmlAction
	Notes                                        []string
}

type htmlChip struct {
	Key, Label, Glyph string
	N                 int
}

type htmlItem struct {
	Key, Label, Title, Check, Where string
	Actions                         []string
	BeforeAfter, Detail, Fix        string
	Diff                            []htmlHunk // a text or payload change, line by line
}

type htmlHunk struct {
	Summary string
	Lines   []compare.DiffLine
}

type htmlAction struct {
	Label, Kind, Error, Tally string
	Messages                  []htmlMessage
	Issues                    int
}

type htmlMessage struct {
	ID, Channel, Glyph, Kind, Status, StatusKey, Time string
	From, To, Subject, Event, Method, URL             string
	Text                                              string
	HTML                                              string // email body for the sandboxed iframe
	Remote                                            bool   // the email loads remote images or CSS
	Body                                              string // pretty-printed payload
	Response                                          string
	Error                                             string
	Via                                               string
	Issues                                            []string
	Attachments                                       []string // "invoice-42.pdf (application/pdf, 84 KB)"
}

// HTML writes a standalone report: one file, opens in any browser.
func HTML(w io.Writer, r Result) error {
	run := r.Run
	d := htmlData{CSP: reportCSP, Version: run.Version, Target: run.Target, Created: run.Started.Local().Format("2 Jan 2006, 15:04"),
		Mode: run.Mode, Compared: r.Compared(), Tally: Tally(run)}
	if r.Compared() {
		c := r.Comparison
		d.Verdict, d.Against = verdict(r), snapshotAt(c.Before)
		d.Tone = "ok"
		switch {
		case c.Count(model.Broke) > 0:
			d.Tone = model.Broke
		case c.Count(model.New) > 0:
			d.Tone = model.New
		case c.Count(model.Changed) > 0:
			d.Tone = model.Changed
		}
		for _, class := range model.Classes {
			info := classInfo[class]
			d.Chips = append(d.Chips, htmlChip{class, info.Label, info.Glyph, c.Count(class)})
		}
		for _, ch := range c.Changes {
			it := htmlItem{Key: ch.Class, Label: classInfo[ch.Class].Label, Title: ch.Title, Check: ch.Check, Where: countLabel(ch.Actions),
				Actions: ch.Actions, BeforeAfter: beforeAfter(ch), Detail: ch.Detail}
			for _, h := range compare.ChangeDiff(ch) {
				it.Diff = append(it.Diff, htmlHunk{Summary: h.Summary(), Lines: h})
			}
			if ch.Class != model.Fixed {
				it.Fix = ch.Fix
			}
			d.Changes = append(d.Changes, it)
		}
		d.Notes = c.Warnings
	} else {
		issues := Issues(run)
		counts := Counts(issues)
		d.Verdict = check.Capitalize(runVerdict(run, counts))
		d.Tone = "ok"
		switch {
		case counts[model.Error] > 0:
			d.Tone = model.Broke
		case counts[model.Warning] > 0:
			d.Tone = model.New
		}
		for _, l := range Levels {
			d.Chips = append(d.Chips, htmlChip{string(l), strings.ToUpper(string(l)) + "S", levelGlyph[l], counts[l]})
		}
		for _, i := range issues {
			d.Issues = append(d.Issues, htmlItem{Key: string(i.Level), Label: strings.ToUpper(string(i.Level)), Title: i.Title, Check: i.Check,
				Where: countLabel(i.Actions), Actions: i.Actions, Detail: i.Detail, Fix: i.Fix})
		}
		d.Notes = run.Warnings
	}
	d.Title = "Echo · " + d.Verdict
	for _, a := range run.Actions {
		ha := htmlAction{Label: a.Label, Kind: a.Kind, Error: a.Error, Tally: Tally(&model.Run{Actions: []model.Action{a}}), Issues: len(a.Issues)}
		byMsg := map[string][]string{}
		for _, i := range a.Issues {
			byMsg[i.Message] = append(byMsg[i.Message], i.Title)
		}
		for _, m := range a.Messages {
			ha.Messages = append(ha.Messages, message(m, byMsg[m.ID]))
		}
		d.Actions = append(d.Actions, ha)
	}
	return htmlTemplate.Execute(w, d)
}

func message(m model.Message, issues []string) htmlMessage {
	hm := htmlMessage{ID: m.ID, Channel: string(m.Channel), Glyph: channelGlyph[m.Channel], Kind: check.Capitalize(check.Kind(m)),
		Status: string(m.Status), StatusKey: string(m.Status), Time: m.At.Local().Format("15:04:05"), From: m.From,
		To: strings.Join(m.To, ", "), Subject: m.Subject, Event: m.Event, Method: m.Method, URL: m.URL, Text: m.Text,
		HTML: m.HTML, Error: m.Error, Via: strings.Trim(m.Source.Via+" · "+m.Source.Plugin, " ·"), Issues: issues}
	if m.HTML != "" {
		// First in the document, so the parser puts it in <head> before
		// anything the email itself has.
		hm.HTML = `<meta http-equiv="Content-Security-Policy" content="` + emailBlocked + `">` + m.HTML
		hm.Remote = remoteRef.MatchString(m.HTML)
	}
	if m.Channel != model.Email {
		hm.Body = pretty(m.Body)
	}
	for _, a := range compare.Attachments(m) {
		hm.Attachments = append(hm.Attachments, a.String())
	}
	if m.Response != nil {
		hm.Response = fmt.Sprintf("%d %s", m.Response.Status, strings.TrimSpace(pretty(m.Response.Body)))
	}
	return hm
}

// pretty indents JSON bodies; anything else is returned as is.
func pretty(s string) string {
	var buf bytes.Buffer
	if json.Indent(&buf, []byte(s), "", "  ") == nil {
		return buf.String()
	}
	return s
}
