// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Command bench is the consent benchmark: it runs rhythm-echo consent
// against every scenario of testbed/consent/scenarios.yaml, good state
// then broken, judges each result against the scenario's expect and
// forbid lists, runs the control pass over every good state, the
// environment probes and the performance measures of PRD 19, then writes
// docs/benchmark-consent.md and testbed/consent/bench/work/results.json.
//
// Run from the module root:
//
//	make build
//	go run -tags testbed ./testbed/consent/bench -bin bin/rhythm-echo [-mode go|docker] [-rounds 10] [-only c01,g01,e05]
//	go run -tags testbed ./testbed/consent/bench -diff a.json b.json
//
// Go mode (the default) serves the scenarios itself on 127.0.0.1:23400
// and reaches them through --map-host; Docker mode runs rhythm-echo in
// the sealed runner container of testbed/consent/docker-compose.yml.
//
// The package builds without the testbed tag, so its tests run with
// every go test ./...; the tag in the commands above is kept for the
// make targets and older notes.
package main
