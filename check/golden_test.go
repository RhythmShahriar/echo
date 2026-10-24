// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package check

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/model"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// scenario is one message from the testbed's s01 to s20 sites, in the
// state the scenario leaves it, with the site it came from.
type scenario struct {
	Scenario string        `json:"scenario"`
	Site     string        `json:"site"`
	Allow    []string      `json:"allow"`
	Message  model.Message `json:"message"`
}

func loadScenarios(t *testing.T) []scenario {
	t.Helper()
	data, err := os.ReadFile("testdata/scenarios.json")
	if err != nil {
		t.Fatal(err)
	}
	var ss []scenario
	if err := json.Unmarshal(data, &ss); err != nil {
		t.Fatal(err)
	}
	for i := range ss {
		// Over Gmail's limit, without a 100 KB line in the fixture.
		ss[i].Message.HTML = strings.ReplaceAll(ss[i].Message.HTML, "BIGHTML", strings.Repeat("<p>x</p>", 13*1024))
	}
	return ss
}

// TestScenariosGolden pins every issue found in the testbed scenarios'
// messages, level, title, detail and fix included, so moving checks
// around (into rules) can't change what users see.
func TestScenariosGolden(t *testing.T) {
	var b strings.Builder
	for _, s := range loadScenarios(t) {
		fmt.Fprintf(&b, "== %s\n", s.Scenario)
		for _, i := range Message(s.Message, Options{SiteHost: s.Site, AllowRecipients: s.Allow}) {
			fmt.Fprintf(&b, "%s %s [%s] %s\n  detail: %s\n  fix: %s\n  key: %s\n", i.Level, i.Check, i.Message, i.Title, i.Detail, i.Fix, i.Key)
		}
	}
	const path = "testdata/scenarios.golden"
	if *update {
		if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if b.String() != string(want) {
		t.Errorf("issues on the scenarios changed (go test ./check -run Golden -update to accept):\n%s", b.String())
	}
}
