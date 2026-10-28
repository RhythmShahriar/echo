// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package consent

import "github.com/RhythmShahriar/echo/model"

// CheckInfo is one consent check as `rhythm echo checks` lists it.
type CheckInfo struct {
	ID    string
	Level model.Level // default
	Doc   string      // one sentence, the FINDS column of `rhythm echo checks`
	Fix   string      // default fix line
}

// The consent check IDs. They stay the same between releases: issue
// keys, checks: levels and ignore: entries name them.
const (
	CheckBeforeChoice       = "consent/before-choice"
	CheckAfterReject        = "consent/after-reject"
	CheckAfterWithdraw      = "consent/after-withdraw"
	CheckRejectGranted      = "consent/reject-granted"
	CheckRejectNotKept      = "consent/reject-not-kept"
	CheckRejectBroken       = "consent/reject-broken"
	CheckNoDefault          = "consent/no-default"
	CheckServerSide         = "consent/server-side"
	CheckScriptBefore       = "consent/script-before"
	CheckDefaultLate        = "consent/default-late"
	CheckV2Signals          = "consent/v2-signals"
	CheckAcceptNotGranted   = "consent/accept-not-granted"
	CheckNoFirstLayerReject = "consent/no-first-layer-reject"
	CheckNoBanner           = "consent/no-banner"
	CheckGPCIgnored         = "consent/gpc-ignored"
	CheckTCFMismatch        = "consent/tcf-mismatch"
	CheckEmbed              = "consent/embed"
	CheckCookielessPing     = "consent/cookieless-ping"
	CheckMetaNoConsentAPI   = "consent/meta-no-consent-api"
	CheckClickFallback      = "consent/click-fallback"
	CheckIgnoreExpired      = "consent/ignore-expired"
)

// Checks is the consent check catalog (PRD 11.1), all 21 in that
// order. Doc says what each finds in one sentence that stands on its
// own, as every other check's does.
var Checks = []CheckInfo{
	{CheckBeforeChoice, model.Error,
		"A tracker that needs consent sent a hit, set a cookie or wrote storage before the visitor chose.",
		"Load the tracker only after the visitor accepts: let the consent banner block its script, or start it with consent denied and grant it on Accept."},
	{CheckAfterReject, model.Error,
		"A tracker that needs consent sent a hit, set a cookie or wrote storage after Reject, on the page, a later page or the reload.",
		"Make Reject stop the tracker: block its script while consent is denied and keep it blocked on every page after the choice."},
	{CheckAfterWithdraw, model.Error,
		"A tracker that needs consent sent a hit, set a cookie or wrote storage after the visitor withdrew consent (run withdraw).",
		"When a visitor withdraws consent, stop the tracker at once and on every later page; withdrawing must be as easy as giving it."},
	{CheckRejectGranted, model.Error,
		"After Reject, a Consent Mode update granted ad_storage or analytics_storage, or a Google hit carried gcs G101, G110 or G111.",
		"Map the banner's Reject to a Consent Mode update with every signal denied; check the CMP's Consent Mode settings or the GTM consent tag."},
	{CheckRejectNotKept, model.Error,
		"After Reject, the banner showed again on a later page or the reload, or the CMP's stored choice was gone.",
		"Keep the visitor's choice: check the CMP cookie's domain, path and expiry and that nothing clears it on the next page."},
	{CheckRejectBroken, model.Error,
		"The Reject button was clicked and didn't take while the CMP's API reject worked, so visitors can't reject (--click button,api).",
		"Fix the Reject button: it must make the same choice the CMP's API does. Look for an overlay over it or a handler that never binds."},
	{CheckNoDefault, model.Error,
		"Google tags sent hits with no gcs and no Consent Mode default was ever set.",
		"Set a Consent Mode default with every signal denied before any Google tag loads, e.g. gtag('consent', 'default', {...}) at the top of the head."},
	{CheckServerSide, model.Error,
		"The site's server sent a Meta Conversions API or GA4 Measurement Protocol event for a visitor who rejected or withdrew (--echo).",
		"Send server-side events only for visitors who accepted: pass the consent state to the server and check it before calling the API."},
	{CheckScriptBefore, model.Error,
		"A tracker script, Google Tag Manager included, loaded before the choice or after Reject (--consent-mode strict).",
		"Under strict, load no tracker script before consent: let the CMP block Google Tag Manager and the other tags until Accept."},
	{CheckDefaultLate, model.Warning,
		"The Consent Mode default was set only after the first Google hit.",
		"Move the Consent Mode default above the Google tag or GTM snippet, so it is set before the first hit."},
	{CheckV2Signals, model.Warning,
		"The Consent Mode default lacks ad_user_data or ad_personalization, or a Google hit's gcd shows them unset.",
		"Add ad_user_data and ad_personalization to the Consent Mode default and update (Consent Mode v2)."},
	{CheckAcceptNotGranted, model.Warning,
		"After Accept, no Consent Mode update granted anything and Google hits still said denied.",
		"Send a Consent Mode update with the accepted signals granted when the visitor accepts."},
	{CheckNoFirstLayerReject, model.Warning,
		"The banner has no Reject on its first layer, so Echo had to open its settings.",
		"Put a Reject button on the banner itself, as visible as Accept."},
	{CheckNoBanner, model.Warning,
		"Trackers that need consent fired or set cookies while no banner showed.",
		"Show a consent banner before these trackers run, or remove them; if the banner is geo-targeted, check with --geo or a proxy in that region."},
	{CheckGPCIgnored, model.Warning,
		"Advertising trackers fired or set cookies for a visitor who sent Global Privacy Control (run gpc).",
		"Treat Global Privacy Control as an opt-out of sale and sharing: many CMPs have a setting to honor it."},
	{CheckTCFMismatch, model.Warning,
		"The CMP's TCF consent string granted a purpose after Reject, or granted none after Accept.",
		"Check the CMP's TCF settings: the TC string must match the choice the visitor made."},
	{CheckEmbed, model.Warning,
		"An embed such as YouTube, Vimeo without dnt=1 or Google Maps loaded or set cookies before the choice or after Reject.",
		"Use youtube-nocookie.com, Vimeo with dnt=1 or a click-to-load placeholder until the visitor accepts."},
	{CheckCookielessPing, model.Notice,
		"Google tags sent cookieless pings before the choice or after Reject (--consent-mode advanced).",
		"These are Consent Mode advanced pings without cookies; switch to basic mode if your policy counts them as tracking."},
	{CheckMetaNoConsentAPI, model.Notice,
		"Meta Pixel fired before the choice or after Reject and never received fbq('consent', ...).",
		"Meta Pixel doesn't read Google Consent Mode. Call fbq('consent', 'revoke') before fbq('init'), then fbq('consent', 'grant') on Accept."},
	{CheckClickFallback, model.Notice,
		"Echo couldn't find the button and made the choice through the CMP's API, so the button itself wasn't tested.",
		"Set --accept-selector or --reject-selector so Echo can test the button visitors use."},
	{CheckIgnoreExpired, model.Notice,
		"An ignore: entry passed its until date and no longer hides its issue.",
		"Fix the issue, or renew the ignore: entry with a new until date and reason."},
}

// Check returns the catalog entry for id.
func Check(id string) (CheckInfo, bool) {
	for _, c := range Checks {
		if c.ID == id {
			return c, true
		}
	}
	return CheckInfo{}, false
}
