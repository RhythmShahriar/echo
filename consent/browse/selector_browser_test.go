// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build browser

package browse

import (
	"errors"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
)

// A --reject-selector the Go parser lets through but the browser refuses
// ("###") stops the check before any page loads, as a usage error that
// names the flag; one the browser takes runs as before.
func TestSelectorRefusedByTheBrowser(t *testing.T) {
	st := shopSite(t, "")
	p := shopPlan(t, st, consent.RunNone, consent.RunReject)
	sel, err := consent.ParseSelector("###")
	if err != nil {
		t.Fatalf("ParseSelector refuses ###, so this test checks nothing: %v", err)
	}
	p.RejectSelector = sel
	_, err = runCheck(t, p, &pageCMP{})
	var fe *consent.FatalError
	if !errors.As(err, &fe) || fe.Code != consent.FatalSelector || !strings.Contains(fe.Detail, `--reject-selector: "###"`) {
		t.Fatalf("err = %v", err)
	}
	if n := st.hits("shop.test", "/"); n != 0 {
		t.Errorf("the page loaded %d times", n)
	}

	ok, err := consent.ParseSelector("#banner >> #reject")
	if err != nil {
		t.Fatal(err)
	}
	p = shopPlan(t, st, consent.RunReject)
	p.RejectSelector = ok
	if _, err := runCheck(t, p, &pageCMP{}); err != nil {
		t.Errorf("a valid selector: %v", err)
	}
}
