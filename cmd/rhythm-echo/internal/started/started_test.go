// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package started

import (
	"testing"
	"time"
)

func TestAtIsSet(t *testing.T) {
	if At.IsZero() || At.After(time.Now()) {
		t.Errorf("At = %v", At)
	}
}
