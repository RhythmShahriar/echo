// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package consent checks a site's cookie consent: it loads pages in a
// browser with no choice, after Accept and after Reject, records every
// tracker hit, cookie and consent signal and judges them. The stages
// (tracker catalog, signal decoders, CMP driver, browser runner,
// verdict) register themselves; RunPlan joins them.
//
// This package holds only the shared types, the checks catalog, the
// stage registry and a few helpers every stage needs (MaskURL,
// EffectiveDefault). It does no I/O and never imports the browser, so
// every stage and writer can depend on it without pulling Chrome in.
package consent
