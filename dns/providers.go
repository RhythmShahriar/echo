// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package dns

import "strings"

// Provider describes a sending service's DNS footprint: the SPF include
// it asks customers to publish, its default DKIM selectors and the
// domains its mail carries (DKIM d=, Return-Path, Received hosts).
type Provider struct {
	Name      string   `json:"name"`                // key used in Sender.Provider
	Label     string   `json:"label"`               // for people
	SPF       []string `json:"spf,omitempty"`       // include: domains, any one of which authorizes it
	Selectors []string `json:"selectors,omitempty"` // DKIM selectors to look up
	Domains   []string `json:"domains,omitempty"`   // what its mail is recognized by
	// Note explains a provider whose selectors can't be guessed.
	Note string `json:"note,omitempty"`
}

// Providers lists the sending services Echo recognizes. Selectors are the
// defaults each service documents; where a service generates them per
// customer (SES, Postmark, SparkPost) only the selector from a captured
// message can be checked.
var Providers = []Provider{
	{Name: "sendgrid", Label: "SendGrid", SPF: []string{"sendgrid.net"}, Selectors: []string{"s1", "s2"},
		Domains: []string{"sendgrid.net", "sendgrid.info"}},
	{Name: "mailgun", Label: "Mailgun", SPF: []string{"mailgun.org"}, Selectors: []string{"smtp", "k1", "mx", "krs", "pic", "mailo"},
		Domains: []string{"mailgun.org", "mailgun.net", "mailgun.info"}},
	{Name: "postmark", Label: "Postmark", SPF: []string{"spf.mtasv.net"}, Selectors: []string{"pm"},
		Domains: []string{"mtasv.net", "postmarkapp.com"},
		Note:    "Postmark selectors are dated (20240101pm); check the s= of a captured message."},
	{Name: "ses", Label: "Amazon SES", SPF: []string{"amazonses.com"},
		Domains: []string{"amazonses.com"},
		Note:    "SES Easy DKIM publishes three random <token>._domainkey CNAMEs; check the s= of a captured message."},
	{Name: "resend", Label: "Resend", SPF: []string{"amazonses.com"}, Selectors: []string{"resend"},
		Domains: []string{"resend.com", "resend.dev"}},
	{Name: "brevo", Label: "Brevo (Sendinblue)", SPF: []string{"spf.brevo.com", "spf.sendinblue.com"}, Selectors: []string{"mail", "brevo1", "brevo2"},
		Domains: []string{"brevo.com", "sendinblue.com", "brevosend.com", "sendibm1.com", "sendibt2.com"}},
	{Name: "mailchimp", Label: "Mailchimp", SPF: []string{"servers.mcsv.net"}, Selectors: []string{"k1", "k2", "k3"},
		Domains: []string{"mcsv.net", "mcdlv.net", "rsgsv.net", "mailchimpapp.net"}},
	{Name: "mandrill", Label: "Mandrill (Mailchimp Transactional)", SPF: []string{"spf.mandrillapp.com"}, Selectors: []string{"mte1", "mte2"},
		Domains: []string{"mandrillapp.com"}},
	{Name: "sparkpost", Label: "SparkPost", SPF: []string{"sparkpostmail.com"}, Selectors: []string{"scph0124"},
		Domains: []string{"sparkpostmail.com", "sparkpost.com"},
		Note:    "SparkPost selectors are generated (scphMMYY); check the s= of a captured message."},
	{Name: "google", Label: "Google Workspace / Gmail", SPF: []string{"_spf.google.com"}, Selectors: []string{"google"},
		Domains: []string{"google.com", "gmail.com", "googlemail.com"}},
	{Name: "microsoft", Label: "Microsoft 365 / Outlook", SPF: []string{"spf.protection.outlook.com"}, Selectors: []string{"selector1", "selector2"},
		Domains: []string{"outlook.com", "protection.outlook.com", "onmicrosoft.com", "hotmail.com"}},
	{Name: "php-mail", Label: "PHP mail() on the web server",
		Note: "mail() hands messages to the server's local MTA, which usually doesn't sign them; the server's IP must be in SPF."},
}

// ProviderByName returns the provider with key name.
func ProviderByName(name string) (Provider, bool) {
	for _, p := range Providers {
		if p.Name == name {
			return p, true
		}
	}
	return Provider{}, false
}

// ProviderForDomain returns the provider whose mail carries host (a DKIM
// d=, Return-Path domain or Received host name).
func ProviderForDomain(host string) (Provider, bool) {
	host = normalize(host)
	if host == "" {
		return Provider{}, false
	}
	for _, p := range Providers {
		for _, d := range p.Domains {
			if host == d || strings.HasSuffix(host, "."+d) {
				return p, true
			}
		}
	}
	return Provider{}, false
}

// commonSelectors is what to try when the sender is unknown: every
// provider's defaults plus generic names, without duplicates.
func commonSelectors() []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, s := range []string{"default", "dkim", "mail"} {
		add(s)
	}
	for _, p := range Providers {
		for _, s := range p.Selectors {
			add(s)
		}
	}
	return out
}
