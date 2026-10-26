// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
)

// Until the verdict lane fills them, every consent writer says it isn't
// built rather than write nothing.
func TestConsentWritersNotBuilt(t *testing.T) {
	var b strings.Builder
	r := &consent.Result{}
	for name, err := range map[string]error{
		"html":    WriteConsentHTML(&b, r, ConsentHTMLOptions{}),
		"junit":   WriteConsentJUnit(&b, r, ""),
		"catalog": WriteConsentCatalog(&b, nil, nil, "cmps", false, ConsentTextOptions{}),
	} {
		if err == nil || err.Error() != "report: consent writer not built yet" {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if b.Len() != 0 {
		t.Errorf("a stub wrote %q", b.String())
	}
}
