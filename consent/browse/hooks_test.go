// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browse

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
)

// hookSignals is the fake decoder that also reads a dataLayer push of
// one Arguments object or array (what gtag pushes) and TCF strings, as
// the real decoder does.
type hookSignals struct{ consenttest.FakeSignals }

func (d hookSignals) Hook(record []byte) (*consent.Signal, error) {
	var h consent.HookRecord
	if err := json.Unmarshal(record, &h); err != nil {
		return nil, err
	}
	if h.Src == "panic" {
		panic("decoder bug")
	}
	if len(h.Args) == 1 {
		var arr []json.RawMessage
		var obj map[string]json.RawMessage
		switch {
		case json.Unmarshal(h.Args[0], &arr) == nil && arr != nil:
			h.Args = arr
		case json.Unmarshal(h.Args[0], &obj) == nil && obj["0"] != nil:
			h.Args = nil
			for i := 0; obj[strconv.Itoa(i)] != nil; i++ {
				h.Args = append(h.Args, obj[strconv.Itoa(i)])
			}
		}
		record, _ = json.Marshal(h)
	}
	return d.FakeSignals.Hook(record)
}

func (hookSignals) TCF(tc string) (*consent.TCF, error) {
	if tc == "bad" {
		return nil, errors.New("TC string too short")
	}
	return &consent.TCF{Version: 2, CMPID: 7, Valid: true}, nil
}

// hookRecorder is a unit recorder with hookSignals linked.
func hookRecorder(t *testing.T, mutate func(*consent.Plan)) *recorder {
	t.Helper()
	consenttest.Use(t, consent.Stages{Classifier: &consenttest.FakeClassifier{}, Signals: hookSignals{}})
	return unitRecorder(t, consent.RunNone, mutate)
}

// startedAgo starts r's clock a second ago, so page times in that
// second are in the past.
func startedAgo(r *recorder) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fallback = wallNow() - 1
}

// pageNow is a page time (ms since the epoch) for run time at.
func pageNow(r *recorder, at int64) float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fallback*1000 + float64(at) + 0.5 // mid-millisecond: floating point never floors it to at-1
}

// A consent record becomes a signal on the run clock; broken, oversized
// and undecodable records are notes; a page time far from now is held
// near now; TCF and GPP events carry what the decoder read.
func TestHookRecords(t *testing.T) {
	r := hookRecorder(t, nil)
	startedAgo(r)
	time := func(at int64) string { return strconv.FormatFloat(pageNow(r, at), 'f', 3, 64) }
	r.hook(`{"t":` + time(5) + `,"src":"datalayer","args":[{"0":"consent","1":"default","2":{"ad_storage":"denied"}}]}`)
	r.hook(`{"t":` + time(10) + `,"src":"gtag","args":["consent","update",{"ad_storage":"granted"}]}`)
	r.hook(`{"t":` + time(11) + `,"src":"datalayer","args":[{"event":"consent"}]}`) // not Consent Mode
	r.hook(`not json`)
	r.hook(`{"t":1,"src":"gtag","args":["` + strings.Repeat("x", consent.MaxHookRecord) + `"]}`)
	r.hook(`{"t":1,"src":"panic"}`)
	r.hook(`{"t":` + time(-60_000) + `,"src":"gtag","args":["consent","update",{"ad_storage":"denied"}]}`)
	r.hook(`{"t":` + time(12) + `,"src":"tcf","event":"useractioncomplete","tcString":"CP` + strings.Repeat("A", 20) + `"}`)
	r.hook(`{"t":` + time(13) + `,"src":"tcf","event":"tcloaded","tcString":"bad"}`)
	r.hook(`{"t":` + time(14) + `,"src":"tcf","event":"other"}`)
	r.hook(`{"t":` + time(15) + `,"src":"gpp","event":"signalStatus` + "\u202e" + `"}`)
	rec := r.finish()
	var got []string
	for _, e := range rec.Events {
		switch e.Kind {
		case consent.EvSignal:
			got = append(got, e.Signal.Source+":"+e.Signal.Command)
			if e.Signal.AtMS != e.AtMS {
				t.Errorf("signal time %d, event %d", e.Signal.AtMS, e.AtMS)
			}
		case consent.EvNote:
			got = append(got, "note:"+e.Note)
		}
	}
	want := []string{"datalayer:default", "gtag:update", "note:unreadable hook record", "note:unreadable hook record",
		"note:unreadable hook record", "gtag:update", "tcf:useractioncomplete", "tcf:tcloaded", "gpp:signalStatus\\u202e"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("events\n%v\nwant\n%v", got, want)
	}
	sig := kindsOf(rec.Events, consent.EvSignal)
	if sig[0].AtMS != 5 || sig[1].AtMS != 10 || sig[0].Signal.State["ad_storage"] != consent.Denied {
		t.Errorf("first signals %+v %+v", sig[0], sig[1])
	}
	if late := sig[2]; late.AtMS != 0 {
		t.Errorf("a record a minute old was kept at %d; want 0, the navigation", late.AtMS)
	}
	if sig[3].Signal.TCF == nil || sig[3].Signal.TCF.CMPID != 7 || sig[4].Signal.TCF != nil {
		t.Errorf("tcf %+v %+v", sig[3].Signal, sig[4].Signal)
	}
}

// Past Limits.HookRecords or Limits.HookBytes records are counted and
// dropped and the run is truncated; hooks.js's own dropped counts add
// up, bounded per report.
func TestHookLimits(t *testing.T) {
	rec := func() *recorder {
		return hookRecorder(t, func(p *consent.Plan) { p.Limits.HookRecords = 3 })
	}()
	for range 5 {
		rec.hook(`{"t":1,"src":"gtag","args":["consent","default",{"ad_storage":"denied"}]}`)
	}
	rec.hook(`{"t":1,"src":"dropped","n":7}`)
	rec.hook(`{"t":1,"src":"dropped","n":-4}`)
	rec.hook(`{"t":1,"src":"dropped","n":99999999999}`)
	out := rec.finish()
	if n := len(kindsOf(out.Events, consent.EvSignal)); n != 3 || out.Dropped != 2+7+maxDroppedReport || !out.Truncated {
		t.Errorf("%d signals, dropped %d, truncated %v", n, out.Dropped, out.Truncated)
	}

	b := hookRecorder(t, func(p *consent.Plan) { p.Limits.HookBytes = 200 })
	one := `{"t":1,"src":"gtag","args":["consent","default",{"ad_storage":"denied"}]}`
	for range 4 {
		b.hook(one)
	}
	out = b.finish()
	if n := len(kindsOf(out.Events, consent.EvSignal)); n != 200/len(one) || !out.Truncated {
		t.Errorf("bytes: %d signals, truncated %v", n, out.Truncated)
	}
}

// A click record notes the click time for the Tab; the queue keeps the
// newest when nobody reads it.
func TestHookClick(t *testing.T) {
	r := hookRecorder(t, nil)
	startedAgo(r)
	for i := range 20 {
		r.hook(`{"t":` + strconv.FormatFloat(pageNow(r, int64(i)), 'f', 3, 64) + `,"src":"click"}`)
	}
	if len(r.clicks) != cap(r.clicks) {
		t.Fatalf("%d clicks queued", len(r.clicks))
	}
	var last int64
	for len(r.clicks) > 0 {
		last = <-r.clicks
	}
	if last != 19 {
		t.Errorf("newest click at %d", last)
	}
	if rec := r.finish(); len(rec.Events) != 0 {
		t.Errorf("clicks became events: %+v", rec.Events)
	}
}

// hooksSource calls hooks.js with the binding name and the limits.
func TestHooksSource(t *testing.T) {
	src := hooksSource(consent.DefaultLimits)
	if !strings.HasPrefix(strings.TrimSpace(hooksJS), "// Copyright") || !strings.Contains(src, "(function (cfg) {") {
		t.Fatal("hooks.js isn't the function expression")
	}
	if !strings.HasSuffix(src, `})({"binding":"__rhythmEchoConsent","maxRecords":2000,"maxBytes":2097152});`+"\n") {
		t.Errorf("source ends %q", src[len(src)-120:])
	}
}

func kindsOf(events []consent.Event, k consent.EventKind) []consent.Event {
	var out []consent.Event
	for _, e := range events {
		if e.Kind == k {
			out = append(out, e)
		}
	}
	return out
}
