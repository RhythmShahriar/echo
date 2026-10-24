// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package kitcheck holds contract tests for what Echo relies on in the
// rhythm kit: masking in kit/secrets, the temp-file cleanup in kit/cache
// and the fail-closed signature check in kit/update. It has no code of
// its own. A kit upgrade that breaks one of these promises fails here,
// before it reaches a capture, a store or a release.
package kitcheck
