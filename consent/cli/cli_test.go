// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestMainPlaceholder(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Main([]string{"https://shop.test"}, Env{Version: "dev", Stdout: &out, Stderr: &errOut}); code != 3 {
		t.Fatalf("exit %d; want 3", code)
	}
	if got := errOut.String(); got != "rhythm echo: consent isn't built yet\n" || out.Len() != 0 {
		t.Fatalf("stderr %q, stdout %q", got, out.String())
	}
	if !strings.HasPrefix(Usage, "rhythm echo consent: ") {
		t.Fatalf("Usage = %q", Usage)
	}
}
