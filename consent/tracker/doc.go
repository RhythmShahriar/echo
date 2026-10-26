// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package tracker is the consent tracker catalog: which requests,
// cookies and storage keys belong to which tracker, what each one is for
// and whether it needs consent. The catalog is YAML embedded in the
// binary; the package registers itself as the consent Classifier
// (consent.SetClassifier).
package tracker
