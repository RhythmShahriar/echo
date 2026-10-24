// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package catcher

import (
	"strings"

	"golang.org/x/net/publicsuffix"
)

// providerDomains are the domains of the APIs Echo recognizes. A
// connector's site host on one of them is never trusted: an older Laravel
// connector reported request()->getHost(), so a forged "Host:
// api.twilio.com" made a sealed Echo let real texts out.
var providerDomains = []string{
	"twilio.com", "sendgrid.com", "sendgrid.net", "stripe.com", "paypal.com", "mailgun.net", "mailgun.org",
	"postmarkapp.com", "resend.com", "amazonaws.com", "brevo.com", "sendinblue.com", "sparkpost.com",
	"mandrillapp.com", "nexmo.com", "vonage.com", "messagebird.com", "bird.com", "plivo.com", "telnyx.com",
	"clicksend.com", "facebook.com", "google-analytics.com", "analytics.google.com", "googleapis.com",
	"onesignal.com", "hubapi.com", "hubspot.com", "hsforms.com", "mailchimp.com", "klaviyo.com", "api-us1.com",
	"activehosted.com", "zapier.com", "make.com", "integromat.com", "n8n.cloud", "slack.com", "discord.com",
	"discordapp.com", "office.com", "office365.com", "logic.azure.com", "api.powerplatform.com", "telegram.org",
}

// provider reports whether host is on or under one of providerDomains.
func provider(host string) bool {
	for _, d := range providerDomains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// publicSuffix reports whether host is itself a public suffix ("co.uk",
// "github.io"), which no one site owns. A name without a dot ("wordpress"
// as a Compose service) is a local name, not a suffix.
func publicSuffix(host string) bool {
	if !strings.Contains(host, ".") {
		return false
	}
	suffix, _ := publicsuffix.PublicSuffix(host)
	return suffix == host
}

// siteHost returns the connector's site host to trust for a request to
// host, or "" when it must be ignored: it names a provider or a public
// suffix, or the request is one a recognizer claims. Each ignored host is
// warned about once.
func (s *Server) siteHost(site, host string, recognized bool) string {
	site = strings.ToLower(hostOnly(site))
	why := ""
	switch {
	case site == "":
		return ""
	case provider(site):
		why = "a provider"
	case publicSuffix(site):
		why = "a public suffix"
	case recognized && strings.EqualFold(site, host):
		why = "a provider"
	default:
		return site
	}
	if _, seen := s.warned.LoadOrStore(site, true); !seen && s.cfg.Warn != nil {
		s.cfg.Warn("connector reported site host " + site + ", which is " + why + "; ignoring")
	}
	return ""
}
