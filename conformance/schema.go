// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package conformance checks a connector against Echo's wire protocol. It
// has three parts:
//
//   - Schema validates captures and decisions against wire/schema.json,
//     the protocol as a JSON Schema generated from package wire.
//   - testdata/*.json holds the shared vectors: capture and decision
//     pairs, including what a connector must do when Echo is unreachable,
//     answers 5xx or times out (fail closed). Connector suites in other
//     languages read them too.
//   - Main runs a strict stand-in Echo (`rhythm echo conformance`) that a
//     connector's end-to-end test points at. It validates every capture
//     it gets, refuses the bad ones, answers the rest like Echo and prints
//     a pass or fail table when it stops.
package conformance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/RhythmShahriar/echo/wire"
)

// Schema validates documents against a JSON Schema in the subset
// conformance/gen writes: type, properties, required,
// additionalProperties, items, enum, minimum, maximum, $ref to $defs and
// x-since (the wire version that added a field).
type Schema struct {
	defs map[string]node
}

type node = map[string]any

// WireSchema is the schema for the wire protocol this Echo speaks.
func WireSchema() (*Schema, error) { return ParseSchema(wire.Schema) }

// ParseSchema reads a schema.
func ParseSchema(b []byte) (*Schema, error) {
	var root struct {
		Defs map[string]node `json:"$defs"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	if len(root.Defs) == 0 {
		return nil, fmt.Errorf("schema: no $defs")
	}
	return &Schema{defs: root.Defs}, nil
}

// Capture returns what is wrong with a capture, as "path: problem" lines;
// none means it follows the protocol. Fields newer than the capture's own
// "v" count as problems: a connector talking to an older Echo must leave
// them out.
func (s *Schema) Capture(doc []byte) []string {
	return s.validate("Capture", doc)
}

// Decision returns what is wrong with Echo's answer to a capture.
func (s *Schema) Decision(doc []byte) []string {
	return s.validate("Decision", doc)
}

func (s *Schema) validate(def string, doc []byte) []string {
	d := json.NewDecoder(bytes.NewReader(doc))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return []string{"not JSON: " + err.Error()}
	}
	version := wire.Version
	if m, ok := v.(map[string]any); ok {
		if n, ok := m["v"].(json.Number); ok {
			if i, err := n.Int64(); err == nil {
				version = int(i)
			}
		}
	}
	c := &check{s: s, version: version}
	c.value(strings.ToLower(def), v, node{"$ref": "#/$defs/" + def})
	return c.problems
}

type check struct {
	s        *Schema
	version  int
	problems []string
}

func (c *check) fail(path, format string, args ...any) {
	c.problems = append(c.problems, path+": "+fmt.Sprintf(format, args...))
}

func (c *check) value(path string, v any, n node) {
	if ref, ok := n["$ref"].(string); ok {
		def, ok := c.s.defs[strings.TrimPrefix(ref, "#/$defs/")]
		if !ok {
			c.fail(path, "the schema has no %s", ref)
			return
		}
		n = def
	}
	switch n["type"] {
	case "object":
		m, ok := v.(map[string]any)
		if !ok {
			c.fail(path, "want an object, got %s", kind(v))
			return
		}
		c.object(path, m, n)
	case "array":
		a, ok := v.([]any)
		if !ok {
			c.fail(path, "want an array, got %s", kind(v))
			return
		}
		items, _ := n["items"].(node)
		for i, e := range a {
			c.value(fmt.Sprintf("%s[%d]", path, i), e, items)
		}
	case "string":
		s, ok := v.(string)
		if !ok {
			c.fail(path, "want a string, got %s", kind(v))
			return
		}
		if enum, ok := n["enum"].([]any); ok && !hasString(enum, s) {
			c.fail(path, "%q is not one of %s", s, join(enum))
		}
	case "integer":
		num, ok := v.(json.Number)
		i, err := num.Int64()
		if !ok || err != nil {
			c.fail(path, "want an integer, got %s", kind(v))
			return
		}
		if lo, ok := n["minimum"].(float64); ok && float64(i) < lo {
			c.fail(path, "%d is below %v", i, lo)
		}
		if hi, ok := n["maximum"].(float64); ok && float64(i) > hi {
			c.fail(path, "%d is above %v, the newest this Echo speaks", i, hi)
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			c.fail(path, "want true or false, got %s", kind(v))
		}
	}
}

func (c *check) object(path string, m map[string]any, n node) {
	props, _ := n["properties"].(node)
	for _, r := range asList(n["required"]) {
		if _, ok := m[r]; !ok {
			c.fail(path+"."+r, "missing (required)")
		}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		p, known := props[k].(node)
		if !known {
			switch extra := n["additionalProperties"].(type) {
			case node:
				c.value(path+"."+k, m[k], extra)
			case bool:
				if !extra {
					c.fail(path+"."+k, "not part of the wire protocol")
				}
			}
			continue
		}
		if since, ok := p["x-since"].(float64); ok && int(since) > c.version {
			c.fail(path+"."+k, "new in wire %d while this capture says v %d: leave it out for an older Echo", int(since), c.version)
			continue
		}
		c.value(path+"."+k, m[k], p)
	}
}

func kind(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case map[string]any:
		return "an object"
	case []any:
		return "an array"
	case string:
		return "a string"
	case json.Number:
		return "a number"
	case bool:
		return "a boolean"
	}
	return fmt.Sprintf("%T", v)
}

func asList(v any) []string {
	a, _ := v.([]any)
	out := make([]string, 0, len(a))
	for _, e := range a {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func hasString(list []any, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}

func join(list []any) string {
	return strings.Join(asList(list), ", ")
}
