// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/testbed/consent/scenarios"
	"github.com/RhythmShahriar/echo/testbed/consent/server"
)

// The outcomes of a scenario, as results.json and the scoreboard give
// them.
const (
	Caught     = "caught"       // a bad scenario's broken state matched every expect and no forbid
	Missed     = "missed"       // it didn't, or matched a forbid
	NotLoaded  = "did not load" // a stand-in the page uses isn't in the run's scripts: a miss, never a catch
	Clean      = "clean"        // a good state with no error or warning outside allow
	FalseAlarm = "false alarm"  // a good state with an error or warning outside allow
	Failed     = "failed"       // the run couldn't be judged: no report, a leak, a forbidden page, an inconclusive good state
	NotRun     = "not run"      // the scenario or tool wasn't run here
)

// Verdict is how one scenario (or one tool on it) did.
type Verdict struct {
	Outcome string `json:"outcome"`
	Found   string `json:"found,omitempty"`  // what matched, for a catch
	Detail  string `json:"detail,omitempty"` // why it missed or failed
}

// Observed is what one run of rhythm-echo consent on a scenario left.
type Observed struct {
	Env     *Envelope    // nil when no report was written
	Exit    int          // the exit code
	Output  string       // stderr, for a failure's detail
	Sink    server.Count // what reached the tracker sink during the run
	Missing []string     // stand-ins the page uses that the run's scripts don't list
}

// judgeBad judges a bad scenario's broken state.
func judgeBad(s scenarios.Scenario, o Observed) Verdict {
	if v, bad := common(s, o); bad {
		return v
	}
	var found, missing []string
	for _, m := range s.Expect {
		if matches(m, o.Env) {
			found = append(found, m.String())
		} else {
			missing = append(missing, m.String())
		}
	}
	titles := strings.Join(o.Env.titles(), "; ")
	if titles == "" {
		titles = "none"
	}
	if len(found) == 0 || (!s.Any && len(missing) > 0) {
		return Verdict{Outcome: Missed, Detail: "no " + strings.Join(missing, " | ") + "; found: " + titles}
	}
	for _, m := range s.Forbid {
		if matches(m, o.Env) {
			return Verdict{Outcome: Missed, Detail: "caught for the wrong reason: " + m.String() + "; found: " + titles}
		}
	}
	return Verdict{Outcome: Caught, Found: strings.Join(found, " | ")}
}

// judgeGood judges a good state: every error and warning outside the
// scenario's allow list is a false alarm (notices never are). A good
// state that couldn't be checked fails.
func judgeGood(s scenarios.Scenario, o Observed) Verdict {
	if v, bad := common(s, o); bad {
		return v
	}
	var alarms []string
	for _, is := range o.Env.Data.Issues {
		if is.Ignored != nil || slices.Contains(s.Allow, is.Check) {
			continue
		}
		if is.Level == model.Error || is.Level == model.Warning {
			alarms = append(alarms, string(is.Level)+" "+is.Check+": "+is.Title)
		}
	}
	if len(alarms) > 0 {
		return Verdict{Outcome: FalseAlarm, Detail: strings.Join(alarms, "; ")}
	}
	if !o.Env.Complete {
		return Verdict{Outcome: Failed, Detail: "inconclusive: " + inconclusive(o.Env)}
	}
	return Verdict{Outcome: Clean}
}

// common is what every judged run must show before its findings count:
// a report, nothing at the sink, no forbidden page opened and every
// stand-in loaded.
func common(s scenarios.Scenario, o Observed) (Verdict, bool) {
	switch {
	case o.Env == nil:
		return Verdict{Outcome: Failed, Detail: fmt.Sprintf("no JSON report (exit %d): %s", o.Exit, tail(o.Output, 300))}, true
	case s.WantSinkZero() && o.Sink.Hits > 0:
		return Verdict{Outcome: Failed, Detail: fmt.Sprintf("%d tracker hits reached the sink in held mode: %s", o.Sink.Hits, byLine(o.Sink.By))}, true
	case o.Sink.Forbidden > 0:
		return Verdict{Outcome: Failed, Detail: fmt.Sprintf("the run opened /logout/ or /cart/ %d times", o.Sink.Forbidden)}, true
	case len(o.Missing) > 0:
		return Verdict{Outcome: NotLoaded, Detail: "not in the run's scripts: " + strings.Join(o.Missing, ", ")}, true
	}
	return Verdict{}, false
}

// matches reports whether e has the finding m names.
func matches(m scenarios.Match, e *Envelope) bool {
	switch {
	case m.IsRun():
		return runMatches(m, e)
	case m.Class != "":
		return changeMatches(m, e)
	}
	return slices.ContainsFunc(e.Data.Issues, func(is consent.Issue) bool {
		return is.Ignored == nil && is.Check == m.Check &&
			(m.Tracker == "" || is.Tracker == m.Tracker) &&
			(m.Level == "" || string(is.Level) == m.Level) &&
			(m.Run == "" || string(is.Run) == m.Run) &&
			(m.Title == "" || containsFold(is.Title, m.Title)) &&
			(m.Status == "" && m.Reason == "")
	})
}

// runMatches finds a run with the status and reason m names. A reason
// is also read from the result's inconclusive list, where a run the
// runner gave up on is reported.
func runMatches(m scenarios.Match, e *Envelope) bool {
	found := false
	e.runs(func(_ consent.Page, r consent.Run) {
		if (m.Run == "" || string(r.Kind) == m.Run) &&
			(m.Status == "" || string(r.Status) == m.Status) &&
			(m.Reason == "" || string(r.Reason) == m.Reason) {
			found = true
		}
	})
	if found || m.Reason == "" || m.Status != "" {
		return found
	}
	return slices.ContainsFunc(e.Data.Inconclusive, func(in consent.Inconclusive) bool {
		return string(in.Reason) == m.Reason && (m.Run == "" || string(in.Run) == m.Run)
	})
}

// changeMatches finds a comparison change of m's class. The check and
// tracker are read from the change's key, check|page|run|tracker.
func changeMatches(m scenarios.Match, e *Envelope) bool {
	c := e.Data.Comparison
	if c == nil {
		return false
	}
	return slices.ContainsFunc(c.Changes, func(ch consent.Change) bool {
		parts := strings.Split(ch.Key, "|")
		for len(parts) < 4 {
			parts = append(parts, "")
		}
		return !ch.Accepted && ch.Class == m.Class &&
			(m.Check == "" || parts[0] == m.Check) &&
			(m.Tracker == "" || parts[3] == m.Tracker) &&
			(m.Run == "" || string(ch.Run) == m.Run) &&
			(m.Title == "" || containsFold(ch.Title, m.Title))
	})
}

// containsFold reports whether s holds sub, ignoring case.
func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}

// inconclusive lists why e is incomplete.
func inconclusive(e *Envelope) string {
	var out []string
	for _, in := range e.Data.Inconclusive {
		out = append(out, string(in.Run)+" "+string(in.Reason))
	}
	e.runs(func(_ consent.Page, r consent.Run) {
		if r.Reason == consent.ReasonNone && r.Status != consent.StatusOK && r.Status != consent.StatusNoBanner {
			out = append(out, string(r.Kind)+" "+string(r.Status))
		}
	})
	slices.Sort(out)
	out = slices.Compact(out)
	if len(out) == 0 {
		return "complete is false"
	}
	return strings.Join(out, ", ")
}

// byLine is a sink's counts by host and path on one line, sorted.
func byLine(by map[string]int) string {
	keys := make([]string, 0, len(by))
	for k, n := range by {
		keys = append(keys, fmt.Sprintf("%s %d", k, n))
	}
	slices.Sort(keys)
	return strings.Join(keys, ", ")
}

// tail is the last n bytes of s, trimmed, for a detail line.
func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = "..." + s[len(s)-n:]
	}
	if s == "" {
		return "no output"
	}
	return strings.ReplaceAll(s, "\n", " / ")
}

// signature is what must stay the same between control rounds of one
// good state: the verdict and the issue keys.
func signature(o Observed) string {
	if o.Env == nil {
		return fmt.Sprintf("no report, exit %d", o.Exit)
	}
	keys := make([]string, 0, len(o.Env.Data.Issues))
	for _, is := range o.Env.Data.Issues {
		keys = append(keys, is.Key)
	}
	slices.Sort(keys)
	return o.Env.Verdict + " [" + strings.Join(keys, ", ") + "]"
}
