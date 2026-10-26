// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browser

import "testing"

func TestPinnedSizeMB(t *testing.T) {
	platform := Platform()
	if platform == "" {
		t.Skip("no Chrome for Testing build for this platform")
	}
	old := pinned
	t.Cleanup(func() { pinned = old })

	// A byte over 1 MB rounds up, as the download line does.
	pinned = Release{Version: "153.0.1.2", Archives: map[string]Archive{platform: {Size: 1<<20 + 1}}}
	if got := PinnedSizeMB(); got != 2 {
		t.Errorf("PinnedSizeMB = %d, want 2", got)
	}
	// No archive for this platform, or one without a size: no number.
	pinned = Release{Version: "153.0.1.2", Archives: map[string]Archive{"elsewhere": {Size: 1 << 20}}}
	if got := PinnedSizeMB(); got != 0 {
		t.Errorf("no archive: PinnedSizeMB = %d, want 0", got)
	}
	pinned = Release{Version: "153.0.1.2", Archives: map[string]Archive{platform: {}}}
	if got := PinnedSizeMB(); got != 0 {
		t.Errorf("no size: PinnedSizeMB = %d, want 0", got)
	}
}
