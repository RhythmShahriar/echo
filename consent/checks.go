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
// order. Doc is the PRD's "Flags" column as plain text.
var Checks = []CheckInfo{
	{CheckBeforeChoice, model.Error,
		"a consent-required tracker hit, tracker cookie or tracker storage key in the before phase of any run, or anywhere in the none run",
		"Load the tracker only after the visitor accepts: let the consent banner block its script, or start it with consent denied and grant it on Accept."},
	{CheckAfterReject, model.Error,
		"the same after the Reject click, on the page, a later page or the reload",
		"Make Reject stop the tracker: block its script while consent is denied and keep it blocked on every page after the choice."},
	{CheckAfterWithdraw, model.Error,
		"the same after the withdraw Reject (run withdraw)",
		"When a visitor withdraws consent, stop the tracker at once and on every later page; withdrawing must be as easy as giving it."},
	{CheckRejectGranted, model.Error,
		"after Reject, a Consent Mode update with ad_storage or analytics_storage granted, or a Google hit with gcs G101, G110 or G111",
		"Map the banner's Reject to a Consent Mode update with every signal denied; check the CMP's Consent Mode settings or the GTM consent tag."},
	{CheckRejectNotKept, model.Error,
		"after Reject, the banner shows again on a later page or the reload, or the CMP's stored state is gone",
		"Keep the visitor's choice: check the CMP cookie's domain, path and expiry and that nothing clears it on the next page."},
	{CheckRejectBroken, model.Error,
		"with --click button,api: the Reject button was found and clicked, the choice wasn't confirmed after retries and the CMP's API reject worked: visitors can't reject",
		"Fix the Reject button: it must make the same choice the CMP's API does. Look for an overlay over it or a handler that never binds."},
	{CheckNoDefault, model.Error,
		"Google hits were sent and no Consent Mode default was seen at any time (hooks) and the hits carry no gcs",
		"Set a Consent Mode default with every signal denied before any Google tag loads, e.g. gtag('consent', 'default', {...}) at the top of the head."},
	{CheckServerSide, model.Error,
		"with --echo: Echo caught a Meta Conversions API or GA4 Measurement Protocol event tagged with the reject or withdraw run (after the click)",
		"Send server-side events only for visitors who accepted: pass the consent state to the server and check it before calling the API."},
	{CheckScriptBefore, model.Error,
		"a tracker script loaded before the choice or after Reject, Google Tag Manager included",
		"Under strict, load no tracker script before consent: let the CMP block Google Tag Manager and the other tags until Accept."},
	{CheckDefaultLate, model.Warning,
		"a Consent Mode default exists but came after the first Google hit",
		"Move the Consent Mode default above the Google tag or GTM snippet, so it is set before the first hit."},
	{CheckV2Signals, model.Warning,
		"the default lacks ad_user_data or ad_personalization, or gcd shows those two unset (l)",
		"Add ad_user_data and ad_personalization to the Consent Mode default and update (Consent Mode v2)."},
	{CheckAcceptNotGranted, model.Warning,
		"after Accept, no update granted any signal and Google hits still say denied (the site loses measurement it was allowed)",
		"Send a Consent Mode update with the accepted signals granted when the visitor accepts."},
	{CheckNoFirstLayerReject, model.Warning,
		"Reject is not on the banner's first layer (Echo had to open settings)",
		"Put a Reject button on the banner itself, as visible as Accept."},
	{CheckNoBanner, model.Warning,
		"consent-required trackers fired or set cookies but no banner showed",
		"Show a consent banner before these trackers run, or remove them; if the banner is geo-targeted, check with --geo or a proxy in that region."},
	{CheckGPCIgnored, model.Warning,
		"in the gpc run, advertising trackers fired or set cookies",
		"Treat Global Privacy Control as an opt-out of sale and sharing: many CMPs have a setting to honor it."},
	{CheckTCFMismatch, model.Warning,
		"a TCF CMP's TC string grants any purpose after Reject, or grants none after Accept",
		"Check the CMP's TCF settings: the TC string must match the choice the visitor made."},
	{CheckEmbed, model.Warning,
		"an embed (YouTube on youtube.com, Vimeo without dnt=1, Google Maps) loaded or set cookies before the choice or after Reject",
		"Use youtube-nocookie.com, Vimeo with dnt=1 or a click-to-load placeholder until the visitor accepts."},
	{CheckCookielessPing, model.Notice,
		"under advanced: Google cookieless pings before the choice or after Reject",
		"These are Consent Mode advanced pings without cookies; switch to basic mode if your policy counts them as tracking."},
	{CheckMetaNoConsentAPI, model.Notice,
		"Meta Pixel fired before the choice or after Reject and no fbq('consent', ...) was seen",
		"Meta Pixel doesn't read Google Consent Mode. Call fbq('consent', 'revoke') before fbq('init'), then fbq('consent', 'grant') on Accept."},
	{CheckClickFallback, model.Notice,
		"the button couldn't be found and the CMP's API made the choice, so the button itself wasn't tested",
		"Set --accept-selector or --reject-selector so Echo can test the button visitors use."},
	{CheckIgnoreExpired, model.Notice,
		"an ignore: entry passed its until date and no longer hides its issue",
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
