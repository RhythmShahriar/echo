// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package headers

import (
	"net/mail"
	"regexp"
	"strings"

	"github.com/RhythmShahriar/echo/dns"
)

// Signals that a header is set by a sending service. Weights: 3 is
// conclusive on its own, 1 only tips a close call.
var headerHints = []struct {
	header   string
	contains string // lowercase substring of the value; "" means present
	provider string
	weight   int
}{
	{"X-Sg-Eid", "", "sendgrid", 3},
	{"X-Sg-Id", "", "sendgrid", 3},
	{"X-Mailgun-Sid", "", "mailgun", 3},
	{"X-Mailgun-Variables", "", "mailgun", 2},
	{"X-Pm-Message-Id", "", "postmark", 3},
	{"X-Ses-Outgoing", "", "ses", 3},
	{"Feedback-Id", "amazonses", "ses", 2},
	{"X-Entity-Ref-Id", "", "resend", 1},
	{"X-Mailer", "mailchimp", "mailchimp", 3},
	{"X-Mc-User", "", "mailchimp", 2},
	{"X-Mandrill-User", "", "mandrill", 3},
	{"X-Mailin-Eid", "", "brevo", 3},
	{"X-Sib-Id", "", "brevo", 3},
	{"X-Msfbl", "", "sparkpost", 2},
	{"X-Google-Smtp-Source", "", "google", 2},
	{"X-Gm-Message-State", "", "google", 2},
	{"X-Ms-Exchange-Crosstenant-Id", "", "microsoft", 1},
	{"X-Php-Originating-Script", "", "php-mail", 3},
	{"X-Mailer", "php/", "php-mail", 2},
}

// Selectors that identify a service even when it signs with the
// customer's domain.
var selectorHints = []struct {
	re       *regexp.Regexp
	provider string
	weight   int
}{
	{regexp.MustCompile(`^google$`), "google", 3},
	{regexp.MustCompile(`^selector[12]$`), "microsoft", 3},
	{regexp.MustCompile(`^resend$`), "resend", 4},
	{regexp.MustCompile(`^\d{8,14}pm$`), "postmark", 3},
	{regexp.MustCompile(`^mte\d$`), "mandrill", 3},
	{regexp.MustCompile(`^scph\d+$`), "sparkpost", 3},
	{regexp.MustCompile(`^brevo\d?$`), "brevo", 3},
	{regexp.MustCompile(`^s[12]$`), "sendgrid", 1},
	{regexp.MustCompile(`^(smtp|krs|mailo|pic)$`), "mailgun", 1},
	{regexp.MustCompile(`^k[123]$`), "mailchimp", 1},
}

// Return-Path local parts: VERP schemes each service uses.
var bounceHints = []struct {
	prefix   string
	provider string
}{
	{"bounces+", "sendgrid"},
	{"bounce+", "mailgun"},
	{"pm_bounces", "postmark"},
	{"msprvs1=", "sparkpost"},
	{"bounce-mc.", "mailchimp"},
	{"bounce-md_", "mandrill"},
}

var reLocalMTA = regexp.MustCompile(`(?i)\bfrom userid \d+|\(postfix, from userid|\bwith local\b|with local-smtp`)

// infer scores each provider on DKIM d= and s=, the Return-Path and the
// Received chain, then picks the best.
func infer(h mail.Header, r *Result) string {
	score := map[string]int{}
	add := func(p string, w int) { score[p] += w }
	byDomain := func(host string, w int) {
		if p, ok := dns.ProviderForDomain(host); ok {
			add(p.Name, w)
		}
	}
	for _, s := range r.Signatures {
		byDomain(s.Domain, 3)
		for _, hint := range selectorHints {
			if hint.re.MatchString(strings.ToLower(s.Selector)) {
				add(hint.provider, hint.weight)
			}
		}
	}
	byDomain(r.Sender.ReturnPathDomain, 3)
	local := strings.ToLower(r.ReturnPath)
	for _, b := range bounceHints {
		if strings.HasPrefix(local, b.prefix) {
			add(b.provider, 2)
		}
	}
	// The receiver's own hops say nothing about the sender: skip "by"
	// hosts in the receiver's organization (mx.google.com receiving mail
	// isn't evidence the sender used Google).
	recv := ""
	if r.Auth.Server != "" {
		recv = dns.OrgDomain(r.Auth.Server)
	}
	seen := map[string]bool{}
	for _, hop := range r.Hops {
		for _, host := range []string{hop.From, hop.By} {
			if host == "" || (host == hop.By && recv != "" && dns.OrgDomain(host) == recv) {
				continue
			}
			if p, ok := dns.ProviderForDomain(host); ok && !seen[p.Name] {
				seen[p.Name] = true
				add(p.Name, 2)
			}
		}
		if reLocalMTA.MatchString(hop.Raw) {
			add("php-mail", 1)
		}
	}
	for _, hint := range headerHints {
		v := h.Get(hint.header)
		if v == "" || (hint.contains != "" && !strings.Contains(strings.ToLower(v), hint.contains)) {
			continue
		}
		add(hint.provider, hint.weight)
	}
	// Resend sends through SES: its mail carries SES's DKIM and Received
	// hosts too. Any Resend signal means Resend.
	if score["resend"] > 0 && score["ses"] > 0 {
		score["resend"] += score["ses"]
	}
	best, bestScore := "", 0
	for _, p := range dns.Providers {
		if score[p.Name] > bestScore {
			best, bestScore = p.Name, score[p.Name]
		}
	}
	if bestScore < 2 {
		return ""
	}
	return best
}
