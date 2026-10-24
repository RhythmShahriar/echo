// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// schema.json must be what the wire types generate: change the types, then
// run go generate ./wire/.
func TestSchemaUpToDate(t *testing.T) {
	got, err := generate("../../wire/wire.go")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("../../wire/schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("wire/schema.json is out of date: run GOWORK=off go generate ./wire/")
	}
}

func TestSchemaShape(t *testing.T) {
	b, err := generate("../../wire/wire.go")
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Version int `json:"x-version"`
		Defs    map[string]struct {
			Required   []string                  `json:"required"`
			Properties map[string]map[string]any `json:"properties"`
			Additional bool                      `json:"additionalProperties"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	if s.Version != 2 {
		t.Errorf("x-version = %d", s.Version)
	}
	for _, name := range []string{"Capture", "Source", "Email", "Attachment", "HTTP", "Decision", "Response"} {
		if _, ok := s.Defs[name]; !ok {
			t.Errorf("no $defs/%s", name)
		}
	}
	c := s.Defs["Capture"]
	if want := `["v","kind","source"]`; mustJSON(c.Required) != want {
		t.Errorf("Capture required = %s, want %s", mustJSON(c.Required), want)
	}
	if c.Properties["v"]["minimum"] != 1.0 || c.Properties["v"]["maximum"] != 2.0 {
		t.Errorf("v = %v", c.Properties["v"])
	}
	if mustJSON(c.Properties["kind"]["enum"]) != `["email","http"]` {
		t.Errorf("kind = %v", c.Properties["kind"])
	}
	if s.Defs["Source"].Properties["site"]["x-since"] != 2.0 || s.Defs["Email"].Properties["attachments"]["x-since"] != 2.0 {
		t.Error("site and attachments are not marked new in wire 2")
	}
	if s.Defs["Source"].Properties["via"]["description"] == nil {
		t.Error("field comments are missing")
	}
	if c.Additional {
		t.Error("captures allow unknown fields")
	}
}

func TestRunWritesTheFile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "schema.json")
	if code := run([]string{"-src", "../../wire/wire.go", "-o", out}, io.Discard); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if code := run([]string{"-src", "missing.go", "-o", out}, io.Discard); code != 1 {
		t.Errorf("missing source: exit %d, want 1", code)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
