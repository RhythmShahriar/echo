// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package server

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// decodeLetters is the gcd letter table of Echo's signal decoder (PRD
// 12.2, the signal lane's S2), written out here rather than imported so
// the testbed checks the decoder's rules and not its code: letter to
// (default, update).
var decodeLetters = map[byte][2]string{
	'l': {"unset", "unset"},
	'p': {"denied", "none"},
	'q': {"denied", "denied"},
	't': {"granted", "none"},
	'r': {"denied", "granted"},
	'm': {"unset", "denied"},
	'n': {"unset", "granted"},
	'u': {"granted", "denied"},
	'v': {"granted", "granted"},
}

var gcdShape = regexp.MustCompile(`^1[13]([a-z])[13]([a-z])[13]([a-z])[13]([a-z])[57]$`)

type gcdCase struct {
	name     string
	defaults map[string]string
	updates  map[string]string
	gcs, gcd string
}

var allDenied = map[string]string{"ad_storage": "denied", "analytics_storage": "denied", "ad_user_data": "denied", "ad_personalization": "denied"}
var allGranted = map[string]string{"ad_storage": "granted", "analytics_storage": "granted", "ad_user_data": "granted", "ad_personalization": "granted"}

var gcdCases = []gcdCase{
	{"no consent mode", nil, nil, "", "13l3l3l3l5"},
	{"default denied", allDenied, nil, "G100", "13p3p3p3p5"},
	{"denied then granted", allDenied, allGranted, "G111", "13r3r3r3r5"},
	{"denied then denied", allDenied, allDenied, "G100", "13q3q3q3q5"},
	{"default granted", allGranted, nil, "G111", "13t3t3t3t5"},
	{"granted then denied", allGranted, allDenied, "G100", "13u3u3u3u5"},
	{"granted then granted", allGranted, allGranted, "G111", "13v3v3v3v5"},
	{"update only granted", nil, allGranted, "G111", "13n3n3n3n5"},
	{"update only denied", nil, allDenied, "G100", "13m3m3m3m5"},
	{"v1 default", map[string]string{"ad_storage": "denied", "analytics_storage": "denied"}, nil, "G100", "13p3p3l3l5"},
	{"analytics only", allDenied, map[string]string{"analytics_storage": "granted"}, "G101", "13p3r3p3p5"},
	{"analytics granted ad denied", allDenied, map[string]string{"analytics_storage": "granted", "ad_storage": "denied"}, "G101", "13q3r3p3p5"},
}

// expectedLetter is the letter the decoder table gives a state.
func expectedLetter(t *testing.T, d, u string) byte {
	t.Helper()
	if d == "" {
		d = "unset"
	}
	if u == "" {
		u = "none"
	}
	if d == "unset" && u == "none" {
		return 'l'
	}
	for l, s := range decodeLetters {
		if s[0] == d && s[1] == u {
			return l
		}
	}
	t.Fatalf("no letter for default %s update %s", d, u)
	return 0
}

// checkDecodes decodes c.gcd with the decoder's rules and compares it
// with the state c describes.
func checkDecodes(t *testing.T, c gcdCase) {
	t.Helper()
	m := gcdShape.FindStringSubmatch(c.gcd)
	if m == nil {
		t.Errorf("%s: %q isn't a gcd the decoder reads", c.name, c.gcd)
		return
	}
	for i, sig := range []string{"ad_storage", "analytics_storage", "ad_user_data", "ad_personalization"} {
		want := expectedLetter(t, c.defaults[sig], c.updates[sig])
		if m[i+1][0] != want {
			t.Errorf("%s: %s letter %c, the decoder reads %c for that state", c.name, sig, m[i+1][0], want)
		}
	}
}

// TestGCDEncodingMatchesDecoder holds the Google stand-in's letter table
// (read from gtag.js) to the decoder's: every (default, update) pair the
// stand-in encodes must decode back to that pair.
func TestGCDEncodingMatchesDecoder(t *testing.T) {
	src, err := fs.ReadFile(Static(), "tags/gtag.js")
	if err != nil {
		t.Fatal(err)
	}
	rows := regexp.MustCompile(`'(\w+),(\w+)': '([a-z])'`).FindAllStringSubmatch(string(src), -1)
	if len(rows) != len(decodeLetters) {
		t.Fatalf("gtag.js has %d letters, the decoder %d", len(rows), len(decodeLetters))
	}
	for _, r := range rows {
		got, ok := decodeLetters[r[3][0]]
		if !ok {
			t.Errorf("gtag.js letter %s: the decoder doesn't know it", r[3])
			continue
		}
		d, u := r[1], r[2]
		if d == "unset" && u == "none" {
			u = "unset" // l: neither set
		}
		if got[0] != d || got[1] != u {
			t.Errorf("gtag.js encodes default %s update %s as %s, which decodes as %v", r[1], r[2], r[3], got)
		}
	}
	for _, c := range gcdCases {
		checkDecodes(t, c)
	}
	for _, public := range []string{"13t3p3p3p5", "13u3q3q3q5", "11p1p1p1p5", "11t1t1t1p5"} {
		if !gcdShape.MatchString(public) {
			t.Errorf("public example %s doesn't fit the shape", public)
		}
	}
	if !strings.Contains(string(src), "'13' + letter('ad_storage') + '3'") {
		t.Error("gtag.js no longer builds gcd as 13<letter>3...5")
	}
}
