// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browse

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/rhythm/kit/secrets"

	"github.com/RhythmShahriar/echo/consent"
)

// eventsPath and harPath are a run's files relative to the evidence
// folder (PRD 9.2).
func eventsPath(page int, kind consent.RunKind) string {
	return fmt.Sprintf("events/page-%d-%s.ndjson", page, kind)
}

func harPath(page int, kind consent.RunKind) string {
	return fmt.Sprintf("har/page-%d-%s.har", page, kind)
}

// createFile creates rel under dir (folders 0700, the file 0600),
// replacing what a failed attempt left.
func createFile(dir, rel string) (*os.File, error) {
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) //nolint:gosec // the evidence folder plus a name Echo makes
}

// eventFile streams one run's events to its NDJSON file, one JSON object
// per line, as they happen. The recorder's writer goroutine is its only
// caller until close.
type eventFile struct {
	rel     string
	f       *os.File
	w       *bufio.Writer
	err     error
	written []eventMark
}

// eventMark is what the file said about an event that the end of the
// run may still change: its phase, its visit and a request's status and
// refusal, which can come after the request was recorded.
type eventMark struct {
	seq     int64
	phase   consent.Phase
	visit   int
	status  int
	refused string
}

// markOf is an event's mark.
func markOf(e consent.Event) eventMark {
	m := eventMark{seq: e.Seq, phase: e.Phase, visit: e.Visit}
	if e.Request != nil {
		m.status, m.refused = e.Request.Status, e.Request.Refused
	}
	return m
}

// openEventFile starts the events file of a run.
func openEventFile(dir string, page int, kind consent.RunKind) (*eventFile, error) {
	rel := eventsPath(page, kind)
	f, err := createFile(dir, rel)
	if err != nil {
		return nil, err
	}
	return &eventFile{rel: rel, f: f, w: bufio.NewWriterSize(f, 64<<10)}, nil
}

// write appends one event as a line. The first error stops the file;
// close reports it.
func (ef *eventFile) write(e consent.Event) {
	if ef.err != nil {
		return
	}
	ef.written = append(ef.written, markOf(e))
	ef.err = writeLine(ef.w, e)
}

// writeLine writes v as one JSON line.
func writeLine(w *bufio.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}

// close flushes and closes the file. When the run's end gave an event
// another phase or visit than the line written (a click whose time
// came late), or a request its status after it was written, the file is
// written again from final, so it always says what the result says.
func (ef *eventFile) close(final []consent.Event) error {
	if ef.err == nil && !sameMarks(ef.written, final) {
		if _, err := ef.f.Seek(0, 0); err != nil {
			ef.err = err
		} else if err := ef.f.Truncate(0); err != nil {
			ef.err = err
		} else {
			ef.w.Reset(ef.f)
			for _, e := range final {
				if ef.err = writeLine(ef.w, e); ef.err != nil {
					break
				}
			}
		}
	}
	if err := ef.w.Flush(); ef.err == nil {
		ef.err = err
	}
	if err := ef.f.Close(); ef.err == nil {
		ef.err = err
	}
	return ef.err
}

// sameMarks reports whether the lines written match the final events.
func sameMarks(written []eventMark, final []consent.Event) bool {
	if len(written) != len(final) {
		return false
	}
	for i, m := range written {
		if m != markOf(final[i]) {
			return false
		}
	}
	return true
}

// harExtra is what a HAR entry needs beyond the recorded request: the
// headers both ways, the response line and the timings. It is kept only
// with --har.
type harExtra struct {
	reqHeaders  proto.NetworkHeaders
	status      int
	statusText  string
	protocol    string
	mimeType    string
	respHeaders proto.NetworkHeaders
	timing      *proto.NetworkResourceTiming
	finished    float64  // Network's monotonic seconds when the body ended; 0 unknown
	size        int      // encoded bytes received
	setCookies  []string // the response's Set-Cookie lines, which Chrome leaves out of its headers
}

// fillResponse copies a response's line, headers and timing.
func (h *harExtra) fillResponse(resp *proto.NetworkResponse) {
	if h == nil || resp == nil {
		return
	}
	h.status, h.statusText, h.protocol, h.mimeType = resp.Status, resp.StatusText, resp.Protocol, resp.MIMEType
	h.respHeaders, h.timing = resp.Headers, resp.Timing
}

// harResponse notes a response for the HAR, with --har.
func (r *recorder) harResponse(id proto.NetworkRequestID, resp *proto.NetworkResponse) {
	if !r.plan.HAR {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if p := r.reqs[id]; p != nil {
		p.har.fillResponse(resp)
	}
}

// harFinished notes when a response's body ended and its size.
func (r *recorder) harFinished(e *proto.NetworkLoadingFinished) {
	if !r.plan.HAR {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if p := r.reqs[e.RequestID]; p != nil && p.har != nil {
		p.har.finished, p.har.size = float64(e.Timestamp), int(e.EncodedDataLength)
	}
}

// newHarExtra starts the HAR data of a request Fetch paused, with
// --har; nil otherwise. setCookies are the Set-Cookie lines of a
// response Echo answered from a --map-host address.
func (r *recorder) newHarExtra(e *proto.FetchRequestPaused, setCookies []string) *harExtra {
	if !r.plan.HAR || e.Request == nil {
		return nil
	}
	return &harExtra{reqHeaders: e.Request.Headers, setCookies: setCookies}
}

// harSetCookies notes a response's raw Set-Cookie lines, with --har.
func (r *recorder) harSetCookies(id proto.NetworkRequestID, lines []string) {
	if !r.plan.HAR || len(lines) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if p := r.reqs[id]; p != nil && p.har != nil {
		p.har.setCookies = lines
	}
}

// noteHarLocked keeps a recorded request's HAR data for the run's file.
func (r *recorder) noteHarLocked(p *pending) {
	if p.har != nil && p.req != nil {
		r.har[p.req] = p.har
	}
}

// HAR 1.2 (http://www.softwareishard.com/blog/har-12-spec/), the parts
// Echo writes. Bodies are never in it.
type (
	harFile struct {
		Log harLog `json:"log"`
	}
	harLog struct {
		Version string     `json:"version"`
		Creator harCreator `json:"creator"`
		Entries []harEntry `json:"entries"`
	}
	harCreator struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	harEntry struct {
		StartedDateTime string      `json:"startedDateTime"`
		Time            float64     `json:"time"`
		Request         harRequest  `json:"request"`
		Response        harResponse `json:"response"`
		Cache           struct{}    `json:"cache"`
		Timings         harTimings  `json:"timings"`
		Type            string      `json:"_type,omitempty"`
		Held            bool        `json:"_held"`
		Mapped          bool        `json:"_mapped"`
		Refused         string      `json:"_refused,omitempty"`
	}
	harRequest struct {
		Method      string    `json:"method"`
		URL         string    `json:"url"`
		HTTPVersion string    `json:"httpVersion"`
		Cookies     []harPair `json:"cookies"`
		Headers     []harPair `json:"headers"`
		QueryString []harPair `json:"queryString"`
		HeadersSize int       `json:"headersSize"`
		BodySize    int       `json:"bodySize"`
	}
	harResponse struct {
		Status      int        `json:"status"`
		StatusText  string     `json:"statusText"`
		HTTPVersion string     `json:"httpVersion"`
		Cookies     []harPair  `json:"cookies"`
		Headers     []harPair  `json:"headers"`
		Content     harContent `json:"content"`
		RedirectURL string     `json:"redirectURL"`
		HeadersSize int        `json:"headersSize"`
		BodySize    int        `json:"bodySize"`
	}
	harContent struct {
		Size     int    `json:"size"`
		MimeType string `json:"mimeType"`
	}
	harPair struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	harTimings struct {
		Blocked float64 `json:"blocked"`
		DNS     float64 `json:"dns"`
		Connect float64 `json:"connect"`
		Send    float64 `json:"send"`
		Wait    float64 `json:"wait"`
		Receive float64 `json:"receive"`
		SSL     float64 `json:"ssl"`
	}
)

// droppedHeaders never reach a HAR, not even masked: they carry the
// visitor's session or a login.
var droppedHeaders = map[string]bool{"cookie": true, "authorization": true, "proxy-authorization": true}

// urlHeaders carry a URL, masked as a stored URL is.
var urlHeaders = map[string]bool{"referer": true, "location": true, "origin": true, "content-location": true}

// maxHeaderValue caps a header value in the HAR.
const maxHeaderValue = 8 << 10

// harRequestHeaders masks a request's headers: Cookie and the logins
// are dropped, URLs masked, the rest through the kit's header masking.
func harRequestHeaders(h proto.NetworkHeaders, showIDs bool) []harPair {
	out := []harPair{}
	for _, name := range sortedNames(h) {
		lower := strings.ToLower(name)
		if droppedHeaders[lower] {
			continue
		}
		out = append(out, harPair{Name: consent.SafeText(name, 200), Value: maskHeader(lower, h[name].Str(), showIDs)})
	}
	return out
}

// harResponseHeaders masks a response's headers: a Set-Cookie keeps the
// cookie's name only.
func harResponseHeaders(h proto.NetworkHeaders, showIDs bool) []harPair {
	out := []harPair{}
	for _, name := range sortedNames(h) {
		lower := strings.ToLower(name)
		if lower == "set-cookie" {
			// Chrome joins several Set-Cookie lines with newlines.
			for line := range strings.SplitSeq(h[name].Str(), "\n") {
				cname, _, _ := strings.Cut(strings.TrimSpace(line), "=")
				if cname = strings.TrimSpace(cname); cname != "" {
					out = append(out, harPair{Name: consent.SafeText(name, 200), Value: consent.SafeText(cname, 256)})
				}
			}
			continue
		}
		if droppedHeaders[lower] {
			continue
		}
		out = append(out, harPair{Name: consent.SafeText(name, 200), Value: maskHeader(lower, h[name].Str(), showIDs)})
	}
	return out
}

// maskHeader masks one header value for the HAR. The whole value is
// masked before it is cut: a secret across the cut would no longer
// match its pattern and leave its first characters in the file.
func maskHeader(lower, v string, showIDs bool) string {
	if urlHeaders[lower] {
		return consent.SafeText(consent.MaskURL(v, showIDs), maxHeaderValue)
	}
	if lower == "cookie" || lower == "set-cookie" {
		v = consent.MaskCookieNames(v, showIDs)
	}
	return consent.SafeText(secrets.RedactHeader(lower, v), maxHeaderValue)
}

// headers1 is a header map of one header. Chrome's map holds JSON
// values, so it is built from JSON.
func headers1(name, value string) proto.NetworkHeaders {
	b, _ := json.Marshal(map[string]string{name: value})
	var h proto.NetworkHeaders
	_ = json.Unmarshal(b, &h)
	return h
}

// sortedNames lists a header map's names in order, so the HAR is the
// same for the same request.
func sortedNames(h proto.NetworkHeaders) []string {
	names := make([]string, 0, len(h))
	for k := range h {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// harQuery lists a masked URL's query as HAR pairs, in order.
func harQuery(masked string) []harPair {
	out := []harPair{}
	u, err := url.Parse(masked)
	if err != nil || u.RawQuery == "" {
		return out
	}
	for kv := range strings.SplitSeq(u.RawQuery, "&") {
		if kv == "" {
			continue
		}
		k, v, _ := strings.Cut(kv, "=")
		if uk, err := url.QueryUnescape(k); err == nil {
			k = uk
		}
		if uv, err := url.QueryUnescape(v); err == nil {
			v = uv
		}
		out = append(out, harPair{Name: consent.SafeText(k, 256), Value: consent.SafeText(v, maxHeaderValue)})
	}
	return out
}

// harTimingsOf turns Chrome's timing (ms from requestTime, -1 for a
// phase that didn't happen) into HAR's, with the body time from when
// loading finished.
func harTimingsOf(t *proto.NetworkResourceTiming, finished float64) (harTimings, float64) {
	h := harTimings{Blocked: -1, DNS: -1, Connect: -1, SSL: -1}
	if t == nil {
		return h, 0
	}
	span := func(a, b float64) float64 {
		if a < 0 || b < a {
			return -1
		}
		return b - a
	}
	h.DNS, h.Connect, h.SSL = span(t.DNSStart, t.DNSEnd), span(t.ConnectStart, t.ConnectEnd), span(t.SslStart, t.SslEnd)
	h.Send = max(span(t.SendStart, t.SendEnd), 0)
	h.Wait = max(span(t.SendEnd, t.ReceiveHeadersEnd), 0)
	for _, first := range []float64{t.DNSStart, t.ConnectStart, t.SendStart} {
		if first >= 0 {
			h.Blocked = first
			break
		}
	}
	if finished > 0 {
		h.Receive = max((finished-t.RequestTime)*1000-t.ReceiveHeadersEnd, 0)
	}
	// Microseconds are as fine as Chrome measures; more digits are float
	// noise.
	for _, v := range []*float64{&h.Blocked, &h.DNS, &h.Connect, &h.Send, &h.Wait, &h.Receive, &h.SSL} {
		*v = math.Round(*v*1000) / 1000
	}
	total := 0.0
	for _, v := range []float64{h.Blocked, h.DNS, h.Connect, h.Send, h.Wait, h.Receive} {
		if v > 0 {
			total += v
		}
	}
	return h, total
}

// harEntryOf is one request's HAR entry. extra may be nil (a request
// whose details Chrome never sent); start is the run's first
// navigation.
func harEntryOf(req *consent.Request, at int64, extra *harExtra, start time.Time, showIDs bool) harEntry {
	e := harEntry{
		StartedDateTime: start.Add(time.Duration(at) * time.Millisecond).UTC().Format(time.RFC3339Nano),
		Request: harRequest{Method: consent.SafeText(req.Method, 20), URL: req.URL, Cookies: []harPair{}, Headers: []harPair{},
			QueryString: harQuery(req.URL), HeadersSize: -1, BodySize: req.BodyBytes},
		Response: harResponse{Status: req.Status, Cookies: []harPair{}, Headers: []harPair{}, HeadersSize: -1, BodySize: -1},
		Type:     req.Type, Held: req.Held, Mapped: req.Mapped, Refused: req.Refused,
	}
	e.Timings, e.Time = harTimingsOf(nil, 0)
	if extra == nil {
		return e
	}
	e.Request.Headers = harRequestHeaders(extra.reqHeaders, showIDs)
	if extra.status != 0 {
		e.Response.Status = extra.status
	}
	e.Response.StatusText = consent.SafeText(extra.statusText, 100)
	e.Response.HTTPVersion = consent.SafeText(strings.ToUpper(extra.protocol), 20)
	e.Request.HTTPVersion = e.Response.HTTPVersion
	e.Response.Headers = harResponseHeaders(extra.respHeaders, showIDs)
	if !slices.ContainsFunc(e.Response.Headers, func(h harPair) bool { return strings.EqualFold(h.Name, "set-cookie") }) {
		for _, line := range extra.setCookies {
			e.Response.Headers = append(e.Response.Headers, harResponseHeaders(headers1("Set-Cookie", line), showIDs)...)
		}
	}
	for _, h := range e.Response.Headers {
		if strings.EqualFold(h.Name, "location") {
			e.Response.RedirectURL = h.Value
		}
	}
	e.Response.Content = harContent{Size: extra.size, MimeType: consent.SafeText(extra.mimeType, 100)}
	if extra.size > 0 {
		e.Response.BodySize = extra.size
	}
	e.Timings, e.Time = harTimingsOf(extra.timing, extra.finished)
	return e
}

// harItem is one request of a run for its HAR.
type harItem struct {
	req   *consent.Request
	at    int64
	extra *harExtra
}

// marshalHAR is the HAR of a run's requests, as indented JSON.
func marshalHAR(items []harItem, start time.Time, version string, showIDs bool) ([]byte, error) {
	if version == "" {
		version = "dev"
	}
	f := harFile{Log: harLog{Version: "1.2", Creator: harCreator{Name: "rhythm-echo", Version: version}, Entries: []harEntry{}}}
	for _, it := range items {
		if it.req != nil {
			f.Log.Entries = append(f.Log.Entries, harEntryOf(it.req, it.at, it.extra, start, showIDs))
		}
	}
	return json.MarshalIndent(f, "", "  ")
}

// writeHAR writes the run's HAR file and returns its path relative to
// the evidence folder.
func writeHAR(dir string, page int, kind consent.RunKind, rec *Recording, version string, showIDs bool) (string, error) {
	var items []harItem
	for _, e := range rec.Events {
		if e.Kind == consent.EvRequest && e.Request != nil {
			items = append(items, harItem{req: e.Request, at: e.AtMS, extra: rec.har[e.Request]})
		}
	}
	b, err := marshalHAR(items, rec.Start, version, showIDs)
	if err != nil {
		return "", err
	}
	rel := harPath(page, kind)
	f, err := createFile(dir, rel)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return "", err
	}
	return rel, f.Close()
}
