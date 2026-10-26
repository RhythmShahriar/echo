// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func get(t *testing.T, h http.Handler, path string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	b, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	return rec.Code, string(b)
}

// Both pages have the banner with both buttons, the Consent Mode default
// before anything else and the update on the choice, and load nothing
// from another host.
func TestPagesShared(t *testing.T) {
	var hits atomic.Int64
	h := newHandler(&hits)
	for _, path := range []string{"/good/", "/broken/", "/good/about"} {
		code, body := get(t, h, path)
		if code != http.StatusOK {
			t.Fatalf("%s: %d", path, code)
		}
		for _, want := range []string{`id="cookie-banner"`, "We use cookies", ">Accept all</button>", ">Reject all</button>",
			`gtag("consent", "default", defaults)`, `gtag("consent", "update"`, `"/tr?id=1234567890&ev=PageView"`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s lacks %q", path, want)
			}
		}
		if strings.Contains(body, "://") || strings.Contains(body, `src="//`) {
			t.Errorf("%s names another host", path)
		}
		if strings.Index(body, `"default"`) > strings.Index(body, "function pixel") {
			t.Errorf("%s: the default comes after the pixel", path)
		}
	}
}

// The good page fires the pixel only after Accept: never on load or after
// Reject.
func TestGoodPage(t *testing.T) {
	var hits atomic.Int64
	_, body := get(t, newHandler(&hits), "/good/")
	if !strings.Contains(body, "var fireAlways = false;") || strings.Contains(body, "var fireAlways = true;") {
		t.Error("the good page fires the pixel on load")
	}
	if !strings.Contains(body, `if (choice === "accepted" && !fireAlways) { pixel(); }`) {
		t.Error("the good page doesn't fire the pixel after Accept")
	}
	if !strings.Contains(body, `href="/good/about"`) {
		t.Error("no link to a second page")
	}
}

// The broken page fires the pixel on every load: before the choice and
// after Reject.
func TestBrokenPage(t *testing.T) {
	var hits atomic.Int64
	_, body := get(t, newHandler(&hits), "/broken/")
	if !strings.Contains(body, "var fireAlways = true;") || !strings.Contains(body, "if (fireAlways) { pixel(); }") {
		t.Error("the broken page doesn't fire the pixel on load")
	}
	if !strings.Contains(body, `href="/broken/about"`) {
		t.Error("no link to a second page")
	}
}

// The pixel counts what reaches it; anything else is 404.
func TestPixelAndHits(t *testing.T) {
	var hits atomic.Int64
	h := newHandler(&hits)
	if _, n := get(t, h, "/hits"); n != "0\n" {
		t.Errorf("hits before any = %q", n)
	}
	code, body := get(t, h, "/tr?id=1234567890&ev=PageView")
	if code != http.StatusOK || !strings.HasPrefix(body, "GIF89a") {
		t.Errorf("/tr: %d %q", code, body)
	}
	if _, n := get(t, h, "/hits"); n != "1\n" {
		t.Errorf("hits after one = %q", n)
	}
	if code, _ := get(t, h, "/$(touch%20/tmp/pwned)"); code != http.StatusNotFound {
		t.Errorf("an unknown path: %d", code)
	}
}
