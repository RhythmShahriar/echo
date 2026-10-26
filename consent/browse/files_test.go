// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browse

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/echo/consent"
)

// fakeToken is a secret-looking value assembled at run time, so no
// literal key sits in the repository.
func fakeToken() string { return "sk_" + "live_" + strings.Repeat("4eC39HqLyjWDarjtT1zdp7dc", 1) }

// headers builds Chrome's header map.
func headers(kv ...string) proto.NetworkHeaders {
	h := proto.NetworkHeaders{}
	for i := 0; i+1 < len(kv); i += 2 {
		for k, v := range headers1(kv[i], kv[i+1]) {
			h[k] = v
		}
	}
	return h
}

// TestNDJSONOneLinePerEvent: every event is one JSON line in order; a
// phase the end of the run changed is written again so the file says
// what the result says.
func TestNDJSONOneLinePerEvent(t *testing.T) {
	dir := t.TempDir()
	f, err := openEventFile(dir, 2, consent.RunReject)
	if err != nil {
		t.Fatal(err)
	}
	events := []consent.Event{
		{Seq: 1, Kind: consent.EvRequest, Phase: consent.PhaseBefore, Request: &consent.Request{Method: "GET", URL: "https://shop.test/"}},
		{Seq: 2, Kind: consent.EvNote, Phase: consent.PhaseBefore, Note: "line one\nline two"},
		{Seq: 3, Kind: consent.EvCookie, Phase: consent.PhaseBefore, Cookie: &consent.Cookie{Name: "_ga", Length: 26}},
	}
	for _, e := range events {
		f.write(e)
	}
	final := append([]consent.Event(nil), events...)
	final[2].Phase = consent.PhaseAfterReject
	if err := f.close(final); err != nil {
		t.Fatal(err)
	}
	if f.rel != "events/page-2-reject.ndjson" {
		t.Errorf("rel = %s", f.rel)
	}
	path := filepath.Join(dir, filepath.FromSlash(f.rel))
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file %v %v", fi, err)
	}
	if di, _ := os.Stat(filepath.Dir(path)); di.Mode().Perm() != 0o700 {
		t.Errorf("folder mode %v", di.Mode().Perm())
	}
	got := readNDJSON(t, path)
	if len(got) != 3 || got[1].Note != "line one\nline two" || got[2].Phase != consent.PhaseAfterReject {
		t.Errorf("lines %+v", got)
	}
	for i, e := range got {
		if e.Seq != int64(i+1) {
			t.Errorf("line %d has seq %d", i, e.Seq)
		}
	}
}

// readNDJSON reads an events file, one event per line.
func readNDJSON(t *testing.T, path string) []consent.Event {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []consent.Event
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var e consent.Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("line %q: %v", sc.Text(), err)
		}
		out = append(out, e)
	}
	return out
}

// sampleHAR is one held hit and one page request with headers both
// ways.
func sampleHAR(token string) []harItem {
	page := &consent.Request{Method: "POST", URL: "https://shop.test/api?q=shoes&page=2", Type: "fetch", Status: 200, BodyBytes: 12}
	hit := &consent.Request{Method: "GET", URL: "https://region1.google-analytics.com/g/collect?v=2&tid=G-%E2%80%A2%E2%80%A2%E2%80%A2%E2%80%A2", Type: "image", Status: 200, Held: true}
	extra := &harExtra{
		reqHeaders: headers("Authorization", "Bearer "+token, "Cookie", "sid="+token, "X-Api-Key", token,
			"Referer", "https://shop.test/reset?token="+token, "Accept", "application/json"),
		status: 200, statusText: "OK", protocol: "h2", mimeType: "application/json",
		respHeaders: headers("Set-Cookie", "sid="+token+"; Path=/; HttpOnly\nlang=de; Path=/", "Content-Type", "application/json",
			"Location", "https://shop.test/next?session="+token),
		timing:   &proto.NetworkResourceTiming{RequestTime: 100, DNSStart: 1, DNSEnd: 3, ConnectStart: 3, ConnectEnd: 10, SslStart: 5, SslEnd: 10, SendStart: 11, SendEnd: 12, ReceiveHeadersEnd: 40},
		finished: 100.050, size: 321,
	}
	return []harItem{{req: page, at: 120, extra: extra}, {req: hit, at: 200}}
}

// TestHARValid: the file is JSON with every field HAR 1.2 requires, the
// creator named and Echo's own fields set.
func TestHARValid(t *testing.T) {
	start := time.Date(2026, 10, 26, 9, 14, 3, 0, time.UTC)
	b, err := marshalHAR(sampleHAR(fakeToken()), start, "1.2.0", false)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	log := doc["log"].(map[string]any)
	if log["version"] != "1.2" || log["creator"].(map[string]any)["name"] != "rhythm-echo" || log["creator"].(map[string]any)["version"] != "1.2.0" {
		t.Errorf("log %v", log)
	}
	entries := log["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("entries %d", len(entries))
	}
	for i, raw := range entries {
		e := raw.(map[string]any)
		for _, k := range []string{"startedDateTime", "time", "request", "response", "cache", "timings", "_held", "_mapped"} {
			if _, ok := e[k]; !ok {
				t.Errorf("entry %d lacks %s", i, k)
			}
		}
		req, resp := e["request"].(map[string]any), e["response"].(map[string]any)
		for _, k := range []string{"method", "url", "httpVersion", "cookies", "headers", "queryString", "headersSize", "bodySize"} {
			if _, ok := req[k]; !ok {
				t.Errorf("entry %d request lacks %s", i, k)
			}
		}
		for _, k := range []string{"status", "statusText", "httpVersion", "cookies", "headers", "content", "redirectURL", "headersSize", "bodySize"} {
			if _, ok := resp[k]; !ok {
				t.Errorf("entry %d response lacks %s", i, k)
			}
		}
		for _, k := range []string{"send", "wait", "receive"} {
			if _, ok := e["timings"].(map[string]any)[k]; !ok {
				t.Errorf("entry %d timings lack %s", i, k)
			}
		}
	}
	first := entries[0].(map[string]any)
	if first["startedDateTime"] != "2026-10-26T09:14:03.12Z" || first["_held"] != false {
		t.Errorf("first entry %v", first)
	}
	q := first["request"].(map[string]any)["queryString"].([]any)
	if len(q) != 2 || q[0].(map[string]any)["name"] != "q" || q[0].(map[string]any)["value"] != "shoes" {
		t.Errorf("queryString %v", q)
	}
	timings := first["timings"].(map[string]any)
	if timings["dns"] != 2.0 || timings["connect"] != 7.0 || timings["ssl"] != 5.0 || timings["wait"] != 28.0 || timings["receive"] != 10.0 {
		t.Errorf("timings %v", timings)
	}
	if second := entries[1].(map[string]any); second["_held"] != true || second["response"].(map[string]any)["status"] != 200.0 {
		t.Errorf("held entry %v", second)
	}
}

// TestHARNoBodiesNoSecrets: no body, cookie value, login or token from
// a header, a Set-Cookie or a URL header reaches the HAR; cookie names
// do.
func TestHARNoBodiesNoSecrets(t *testing.T) {
	token := fakeToken()
	b, err := marshalHAR(sampleHAR(token), time.Now(), "", false)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, token) || strings.Contains(s, "4eC39HqLyjWD") {
		t.Errorf("the token is in the HAR:\n%s", s)
	}
	for _, gone := range []string{`"Authorization"`, `"Cookie"`, `"postData"`, `"text"`} {
		if strings.Contains(s, gone) {
			t.Errorf("%s is in the HAR", gone)
		}
	}
	for _, kept := range []string{`"value": "sid"`, `"value": "lang"`, `"name": "X-Api-Key"`, `"version": "dev"`} {
		if !strings.Contains(s, kept) {
			t.Errorf("%s is missing from the HAR", kept)
		}
	}
}

// FuzzHAR: the writer emits valid JSON for any request it is given.
func FuzzHAR(f *testing.F) {
	f.Add("GET", "https://shop.test/?a=1&b=%zz", "X-Token", "abc", 200, "h2", "x=1")
	f.Add("POST", "::not a url", "Set-Cookie", "a=b\nc=d", 0, "", "")
	f.Add("\xff\xfe", "https://x.test/\u202e", "\x00", "\xc0", -1, "http/1.1", "\n=\n")
	f.Fuzz(func(t *testing.T, method, raw, name, value string, status int, protocol, setCookie string) {
		req := &consent.Request{Method: method, URL: raw, Type: name, Status: status, Refused: value}
		extra := &harExtra{reqHeaders: headers(name, value, "Cookie", value), status: status, statusText: value, protocol: protocol,
			mimeType: name, respHeaders: headers(name, value, "Set-Cookie", setCookie, "Location", raw)}
		b, err := marshalHAR([]harItem{{req: req, at: int64(status), extra: extra}, {req: req}}, time.Unix(0, 0), protocol, false)
		if err != nil {
			t.Fatal(err)
		}
		if !json.Valid(b) {
			t.Fatalf("not JSON: %s", b)
		}
	})
}

// TestNDJSONLateStatus: a request whose status came after its line was
// written is written again with it.
func TestNDJSONLateStatus(t *testing.T) {
	dir := t.TempDir()
	f, err := openEventFile(dir, 1, consent.RunNone)
	if err != nil {
		t.Fatal(err)
	}
	req := consent.Request{Method: "GET", URL: "https://shop.test/old"}
	f.write(consent.Event{Seq: 1, Kind: consent.EvRequest, Request: &req})
	final := []consent.Event{{Seq: 1, Kind: consent.EvRequest, Request: &consent.Request{Method: "GET", URL: "https://shop.test/old", Status: 301}}}
	if err := f.close(final); err != nil {
		t.Fatal(err)
	}
	got := readNDJSON(t, filepath.Join(dir, filepath.FromSlash(f.rel)))
	if len(got) != 1 || got[0].Request.Status != 301 {
		t.Errorf("lines %+v", got)
	}
}

// TestMaskHeaderMasksBeforeCut: a secret across the HAR's header cut is
// masked whole, so no leading fragment of it lands in the file; a plain
// long value is still cut.
func TestMaskHeaderMasksBeforeCut(t *testing.T) {
	key := "sk_" + "live_" + strings.Repeat("Q7x", 10) // assembled: a literal key trips push protection
	pad := strings.Repeat("a", maxHeaderValue-10) + " "
	got := maskHeader("x-debug", pad+key+" tail", false)
	if strings.Contains(got, "sk_live") || strings.Contains(got, "sk_li") || strings.Contains(got, "Q7xQ7x") {
		t.Errorf("a fragment of the secret is left: ...%q", got[max(0, len(got)-40):])
	}
	if len(got) > maxHeaderValue+64 {
		t.Errorf("len = %d; want it cut near %d", len(got), maxHeaderValue)
	}
	// Negative: a value with no secret keeps its text up to the cut.
	plain := maskHeader("x-debug", strings.Repeat("b", 2*maxHeaderValue), false)
	if !strings.HasPrefix(plain, strings.Repeat("b", 64)) || len(plain) > maxHeaderValue+64 {
		t.Errorf("plain value: len %d", len(plain))
	}
}
