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
package signal
