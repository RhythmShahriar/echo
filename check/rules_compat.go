// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package check

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/RhythmShahriar/echo/compat"
	"github.com/RhythmShahriar/echo/model"
)

// compatHTML reports the HTML and CSS in an email that common clients
// drop, from the caniemail.com table in the compat package. Only
// features a client drops outright count: a feature only partly
// supported somewhere (a <style> block in Gmail) is in almost every
// email and would bury the rest. The title names the feature and the
// clients, not where it is, so a comparison sees the same issue after an
// unrelated edit.
var compatHTML = Rule{
	ID: "compat/html", Level: model.Notice, Channels: emailOnly,
	Doc: "An email's HTML uses a feature some common email clients drop (caniemail.com).",
	Run: func(m model.Message, _ Options) []model.Issue {
		if m.HTML == "" {
			return nil
		}
		var out []model.Issue
		for _, f := range compat.HTML(m.HTML) {
			if len(f.Unsupported) == 0 {
				continue
			}
			out = append(out, model.Issue{
				Title:  fmt.Sprintf("Email uses %s, which %s drop", f.Feature, andList(f.Unsupported)),
				Detail: f.String(),
				Fix:    f.Note,
			})
		}
		return out
	},
}

// SpamHeader is the header up stores SpamAssassin's verdict on an email
// in when it runs with --spamassassin (SpamValue writes it), so the
// compat/spam check can report it wherever the message is shown.
const SpamHeader = "X-Echo-Spamassassin"

// SpamValue is SpamHeader's value for a spamd verdict, or for the error
// that kept spamd from giving one.
func SpamValue(r compat.SpamResult, err error) string {
	if err != nil {
		return "error=" + strings.ReplaceAll(err.Error(), ";", ",")
	}
	spam := "no"
	if r.Spam {
		spam = "yes"
	}
	return fmt.Sprintf("score=%s; threshold=%s; spam=%s; rules=%s", num(r.Score), num(r.Threshold), spam, strings.Join(r.Rules, ","))
}

func num(f float64) string { return strconv.FormatFloat(f, 'f', 1, 64) }

// compatSpam reports what SpamAssassin made of an email: its score, or
// why it couldn't score it. Notice level even when spamd calls it spam:
// a staging email often lacks what a real one has (DKIM, a real From).
var compatSpam = Rule{
	ID: "compat/spam", Level: model.Notice, Channels: emailOnly,
	Doc: "What SpamAssassin scores an email, when up runs with --spamassassin.",
	Run: func(m model.Message, _ Options) []model.Issue {
		v := m.Headers[SpamHeader]
		if v == "" {
			return nil
		}
		f := map[string]string{}
		for _, part := range strings.Split(v, ";") {
			k, val, _ := strings.Cut(strings.TrimSpace(part), "=")
			f[k] = val
		}
		if e, failed := f["error"]; failed {
			return []model.Issue{{Title: "SpamAssassin couldn't score the email", Detail: e,
				Fix: "Check that spamd runs and answers at the --spamassassin address."}}
		}
		title := fmt.Sprintf("SpamAssassin scores the email %s of %s", f["score"], f["threshold"])
		if f["spam"] == "yes" {
			title = fmt.Sprintf("SpamAssassin calls the email spam (%s of %s)", f["score"], f["threshold"])
		}
		detail := "no rules hit"
		if r := f["rules"]; r != "" {
			detail = "rules: " + strings.ReplaceAll(r, ",", ", ")
		}
		return []model.Issue{{Title: title, Detail: detail}}
	},
}

// andList joins items as "a", "a and b" or "a, b and c".
func andList(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}
