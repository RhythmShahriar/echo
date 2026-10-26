// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package signal decodes consent signals: Google's gcs and gcd
// parameters, Consent Mode and other consent calls recorded by the page
// hooks, IAB TCF and GPP strings and each CMP's stored state. Every
// parser is pure Go over bytes with a fuzz target. It registers the
// consent SignalDecoder and StateReader.
//
// Google documents neither gcs nor gcd. The gcs reading (G1 plus one
// digit each for ad_storage and analytics_storage) is the one third-party
// documentation agrees on. The gcd letter table is unofficial and
// community-documented, so every consumer prints GCDUnofficial beside it
// and the raw value; it follows:
//
//   - Stape: https://stape.io/blog/google-consent-mode-v2
//   - Didomi (the only source for the letter e): https://support.didomi.io/how-to-check-your-google-consent-mode-v2-advanced-implementation-on-web
//   - Complianz: https://complianz.io/consent-mode-for-7-0
//
// # Decoders
//
// Each decoder, what it reads and the sources it follows:
//
//   - gcs (Decoder.GCS): the gcs query parameter of Google hits. Sources:
//     Google's consent debugging guide
//     (https://developers.google.com/tag-platform/security/guides/consent-debugging)
//     and the Stape and Didomi pages above.
//   - gcd (Decoder.GCD): the gcd query parameter. Unofficial: Google does
//     not document it. Sources: the Stape, Didomi and Complianz pages
//     above. testdata/gcd.yaml says where each fixture came from and when;
//     testdata/captures/README.md is how real hits are added.
//   - Consent Mode (Decoder.Hook): gtag('consent', ...) and
//     dataLayer.push(['consent', ...]) with region and wait_for_update,
//     and gtag('set', ...) for ads_data_redaction and url_passthrough.
//     Source:
//     https://developers.google.com/tag-platform/security/concepts/consent-mode
//   - Meta Pixel (Decoder.Hook): fbq('consent', 'revoke'|'grant') and
//     fbq('init', ...). Source:
//     https://developers.facebook.com/docs/meta-pixel/implementation/gdpr
//   - Microsoft UET (Decoder.Hook): uetq.push('consent', ...), read as
//     Microsoft Advertising's UET consent mode help describes it.
//   - IAB TCF v2.2 (Decoder.TCF): the core segment of a TC string from
//     __tcfapi or the euconsent-v2 cookie. Source: the IAB Tech Lab
//     consent string and vendor list formats v2 specification.
//   - GPP and US Privacy (Decoder.GPP): the GPP header, the US sections'
//     sale and sharing opt-outs and the bare US Privacy string. Source:
//     the IAB Tech Lab Global Privacy Platform consent string
//     specification.
//   - CMP stored state (the StateReader and package cmpstate): Cookiebot,
//     OneTrust, CookieYes, Complianz, Usercentrics, Borlabs, Didomi,
//     iubenda and TCF cookies, storage keys and API state. Each parser
//     cites the CMP's own documentation of its cookie or API.
//
// Every parser has positive and negative fixtures and a fuzz target.
package signal
