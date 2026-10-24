// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package kitcheck

import (
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/rhythm/kit/secrets"
)

// join builds credential-shaped fixtures at run time, so the source holds
// nothing a secret scanner would flag.
func join(parts ...string) string { return strings.Join(parts, "") }

// TestRedactMasks covers the shapes Echo stores every day: a WordPress
// password reset link, a Telegram bot API URL and a JSON Web Token.
func TestRedactMasks(t *testing.T) {
	resetKey := "Zq9AbCdEf123GhIjK"
	botToken := join("AA", "HdqTcvCH1vGWJxfSeofSAs0K5PALDsawz")
	jwt := join("eyJ", "hbGciOiJIUzI1NiJ9.", "eyJ", "zdWIiOiIxMjM0NTY3ODkwIn0.", "dBjftJeZ4CVPmB92K27uhbUJU1p1r")
	for _, c := range []struct{ name, in, gone string }{
		{"wordpress reset key", "https://shop.test/wp-login.php?action=rp&key=" + resetKey + "&login=ann", resetKey},
		{"reset key in HTML", `<a href="https://shop.test/wp-login.php?action=rp&amp;key=` + resetKey + `&amp;login=ann">`, resetKey},
		{"telegram bot token", "https://api.telegram.org/bot123456789:" + botToken + "/sendMessage", botToken},
		{"jwt", "Bearer token " + jwt, jwt},
	} {
		out := secrets.Redact(c.in)
		if strings.Contains(out, c.gone) || out == c.in {
			t.Errorf("%s: Redact(%q) = %q, the secret is still there", c.name, c.in, out)
		}
	}
}

// TestRedactKeeps covers ordinary links Echo compares: their query
// values are no secret and a masked one would hide a real change.
func TestRedactKeeps(t *testing.T) {
	for _, in := range []string{
		"https://shop.test/shop/?page=2",
		"https://shop.test/?orderby=price&order=asc&page=2",
		"Your order 1042 has shipped to 12 Main Street.",
	} {
		if out := secrets.Redact(in); out != in {
			t.Errorf("Redact(%q) = %q, want it unchanged", in, out)
		}
	}
}

// TestRedactHeader masks credential headers by name, whatever the value
// looks like, and leaves the rest readable.
func TestRedactHeader(t *testing.T) {
	for _, name := range []string{"x-auth-token", "X-Auth-Token", "Authorization", "X-Api-Key", "X-WP-Nonce"} {
		if got := secrets.RedactHeader(name, "abc"); strings.Contains(got, "abc") {
			t.Errorf("RedactHeader(%q, abc) = %q, want it masked", name, got)
		}
	}
	if got := secrets.RedactHeader("Content-Type", "text/html"); got != "text/html" {
		t.Errorf("RedactHeader(Content-Type) = %q, want it unchanged", got)
	}
}

// TestMaskKeepsFour is the most a masked value shows, as Echo's README
// promises: at most 4 leading characters, never more than a third.
func TestMaskKeepsFour(t *testing.T) {
	v := join("sk", "_live_", "51AbCdEfGhIjKlMnOpQrStUv")
	if got := secrets.Mask(v, 8); got != v[:4]+"•••••" {
		t.Errorf("Mask(v, 8) = %q, want %q", got, v[:4]+"•••••")
	}
	if got := secrets.Mask("abcdef", 4); got != "ab•••••" {
		t.Errorf("Mask(abcdef, 4) = %q, want a third kept", got)
	}
}

// TestPrefilter checks that text with nothing secret-shaped skips most
// rules: 1 MiB of plain prose must take a fraction of the time the same
// size takes when every line holds secrets. The ceiling catches a return
// to the v0.4 cost (about 120 ms per 256 KiB). Echo's target is 20 ms per
// MiB, which v0.6.0 does not reach yet (a kit follow-up); timings are
// skipped under -race.
func TestPrefilter(t *testing.T) {
	if raceOn || testing.Short() {
		t.Skip("timing test")
	}
	const size = 1 << 20
	clean := strings.Repeat("Hello there, your parcel is on its way and will arrive on Monday morning.\n", size/75)
	dirty := strings.Repeat("password=Zq9AbCdEf123 https://u:Zq9AbCdEf@db.test token: Zq9AbCdEf123GhIjK\n", size/76)
	best := func(s string) time.Duration {
		d := time.Hour
		for range 3 {
			start := time.Now()
			if out := secrets.Redact(s); len(out) == 0 {
				t.Fatal("empty output")
			}
			d = min(d, time.Since(start))
		}
		return d
	}
	c, d := best(clean), best(dirty)
	t.Logf("1 MiB clean: %s, with secrets: %s", c, d)
	if c*4 > d {
		t.Errorf("clean text took %s, more than a quarter of the %s text with secrets took: the prefilter isn't skipping rules", c, d)
	}
	if c > 250*time.Millisecond {
		t.Errorf("1 MiB of clean text took %s, want under 250 ms", c)
	}
}
