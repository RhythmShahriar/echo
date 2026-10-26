// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package consent

import (
	"fmt"
	"strings"
	"sync"
)

// Stage names one part of a consent check that another package
// provides and registers.
type Stage string

// The stages, by the name messages give them.
const (
	StageClassifier Stage = "tracker"
	StageSignals    Stage = "signal"
	StageState      Stage = "state reader"
	StageCMP        Stage = "cmp"
	StageRunner     Stage = "browse"
	StageEvaluator  Stage = "verdict"
	StageComparer   Stage = "compare"
)

// Stages are the registered implementations of every stage; a nil field
// is a stage not linked into this build.
type Stages struct {
	Classifier Classifier
	Signals    SignalDecoder
	State      StateReader
	CMP        CMPDriver
	Runner     Runner
	Evaluator  Evaluator
	Comparer   Comparer
}

var (
	stagesMu sync.RWMutex
	stages   Stages
)

// set registers one stage, refusing a second registration: two packages
// claiming one stage is a build mistake, never something to pick from.
func set[T any](field *T, v T, name Stage) {
	stagesMu.Lock()
	defer stagesMu.Unlock()
	if any(*field) != nil {
		panic(fmt.Sprintf("consent: %s registered twice", name))
	}
	*field = v
}

// SetClassifier registers the tracker catalog. It panics when one is
// already registered.
func SetClassifier(c Classifier) { set(&stages.Classifier, c, StageClassifier) }

// SetSignals registers the signal decoders. It panics when they are
// already registered.
func SetSignals(s SignalDecoder) { set(&stages.Signals, s, StageSignals) }

// SetStateReader registers the CMP state reader. It panics when one is
// already registered.
func SetStateReader(s StateReader) { set(&stages.State, s, StageState) }

// SetCMP registers the CMP driver. It panics when one is already
// registered.
func SetCMP(c CMPDriver) { set(&stages.CMP, c, StageCMP) }

// SetRunner registers the browser runner. It panics when one is already
// registered.
func SetRunner(r Runner) { set(&stages.Runner, r, StageRunner) }

// SetEvaluator registers the verdict. It panics when one is already
// registered.
func SetEvaluator(e Evaluator) { set(&stages.Evaluator, e, StageEvaluator) }

// SetComparer registers the comparison. It panics when one is already
// registered.
func SetComparer(c Comparer) { set(&stages.Comparer, c, StageComparer) }

// Linked returns the registered stages.
func Linked() Stages {
	stagesMu.RLock()
	defer stagesMu.RUnlock()
	return stages
}

// Swap replaces the registered stages for a test (nil fields keep the
// current one) and returns a func that restores them. Only
// consenttest.Use calls it; tests using it don't run in parallel.
func Swap(s Stages) (restore func()) {
	stagesMu.Lock()
	defer stagesMu.Unlock()
	old := stages
	if s.Classifier != nil {
		stages.Classifier = s.Classifier
	}
	if s.Signals != nil {
		stages.Signals = s.Signals
	}
	if s.State != nil {
		stages.State = s.State
	}
	if s.CMP != nil {
		stages.CMP = s.CMP
	}
	if s.Runner != nil {
		stages.Runner = s.Runner
	}
	if s.Evaluator != nil {
		stages.Evaluator = s.Evaluator
	}
	if s.Comparer != nil {
		stages.Comparer = s.Comparer
	}
	return func() {
		stagesMu.Lock()
		defer stagesMu.Unlock()
		stages = old
	}
}

// Need returns `consent: <stage> not linked into this build` when a
// stage is missing (several are listed, comma-separated), else nil.
func Need(s ...Stage) error {
	l := Linked()
	var missing []string
	for _, st := range s {
		if !l.has(st) {
			missing = append(missing, string(st))
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("consent: %s not linked into this build", strings.Join(missing, ", "))
}

// has reports whether stage st is registered in l.
func (l Stages) has(st Stage) bool {
	switch st {
	case StageClassifier:
		return l.Classifier != nil
	case StageSignals:
		return l.Signals != nil
	case StageState:
		return l.State != nil
	case StageCMP:
		return l.CMP != nil
	case StageRunner:
		return l.Runner != nil
	case StageEvaluator:
		return l.Evaluator != nil
	case StageComparer:
		return l.Comparer != nil
	}
	return false
}
