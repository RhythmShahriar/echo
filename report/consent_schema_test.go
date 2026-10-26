// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/model"
)

// The consent schemas are generated from the Go types by reflection, so
// they describe the JSON exactly as the types serialise it: a field
// without omitempty is required, a slice, map or pointer without it may
// be null, and every typed string lists its values. TestConsentSchemas
// fails when a type changes; -update rewrites the files.

// schemaEnums are the values of every typed string the consent kinds
// carry.
var schemaEnums = map[reflect.Type][]string{
	reflect.TypeFor[consent.RunKind]():     strs(consent.AllRuns),
	reflect.TypeFor[consent.Phase]():       {"before", "after-accept", "after-reject", "gpc", "after-withdraw"},
	reflect.TypeFor[consent.Policy]():      {"advanced", "basic", "strict"},
	reflect.TypeFor[consent.Category]():    {"analytics", "advertising", "session-replay", "social", "embed", "functional", "necessary"},
	reflect.TypeFor[consent.ConsentNeed](): {"needed", "exempt", "no"},
	reflect.TypeFor[consent.HitKind]():     {"beacon", "script", "frame"},
	reflect.TypeFor[consent.RunStatus](): {"ok", "no-banner", "no-button", "click-failed", "load-failed", "timeout",
		"navigated-away", "limit-reached", "canceled"},
	reflect.TypeFor[consent.Reason](): {"", "no_banner", "cmp_not_found", "no_button", "click_missed", "state_unchanged",
		"navigation_failed", "timeout", "limit", "canceled"},
	reflect.TypeFor[consent.Value](): {"granted", "denied", "unset", "unknown"},
	reflect.TypeFor[model.Level]():   {"error", "warning", "notice"},
}

// schemaFieldEnums are plain string fields that still take a fixed set
// of values, by type and field name.
var schemaFieldEnums = map[string][]string{
	"Change.Class": {consent.ClassBroke, consent.ClassNew, consent.ClassChanged, consent.ClassFixed},
}

func strs[T ~string](s []T) []string {
	out := make([]string, len(s))
	for i, v := range s {
		out[i] = string(v)
	}
	return out
}

// schemaGen builds one schema's $defs.
type schemaGen struct {
	defs map[string]any
}

// defName is a type's name in $defs: its Go name with a lower-case
// first letter (detectResult, cmpInfo).
func defName(t reflect.Type) string {
	n := t.Name()
	i := 0
	for i < len(n) && unicode.IsUpper(rune(n[i])) {
		i++
	}
	if i > 1 && i < len(n) {
		i-- // CMPInfo -> cmpInfo, keeping the I of Info
	}
	return strings.ToLower(n[:i]) + n[i:]
}

// of is the schema of a value of type t; nullable says nil encodes as
// null (a slice, map or pointer without omitempty).
func (g *schemaGen) of(t reflect.Type, nullable bool) map[string]any {
	if vals, ok := schemaEnums[t]; ok {
		return map[string]any{"type": "string", "enum": vals}
	}
	if t == reflect.TypeFor[time.Time]() {
		return map[string]any{"type": "string", "format": "date-time"}
	}
	typ := func(name string) any {
		if nullable {
			return []any{name, "null"}
		}
		return name
	}
	switch t.Kind() {
	case reflect.Pointer:
		if t.Elem().Kind() == reflect.Struct {
			return map[string]any{"$ref": "#/$defs/" + g.def(t.Elem(), nullable)}
		}
		return g.of(t.Elem(), nullable)
	case reflect.Struct:
		return map[string]any{"$ref": "#/$defs/" + g.def(t, false)}
	case reflect.Slice:
		return map[string]any{"type": typ("array"), "items": g.of(t.Elem(), false)}
	case reflect.Map:
		return map[string]any{"type": typ("object"), "additionalProperties": g.of(t.Elem(), false)}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	}
	return map[string]any{} // any: the schema says nothing
}

// def adds struct type t to $defs (and its nullable twin, xOrNull, when
// a pointer to it may be null) and returns its name.
func (g *schemaGen) def(t reflect.Type, nullable bool) string {
	name := defName(t)
	if nullable {
		name += "OrNull"
	}
	if _, ok := g.defs[name]; ok {
		return name
	}
	g.defs[name] = nil // a placeholder, so a type that holds itself ends
	props, required := map[string]any{}, []any{}
	g.fields(t, props, &required)
	var typ any = "object"
	if nullable {
		typ = []any{"object", "null"}
	}
	d := map[string]any{"type": typ, "properties": props}
	if len(required) > 0 {
		d["required"] = required
	}
	g.defs[name] = d
	return name
}

// fields adds t's JSON fields, embedded structs flattened as
// encoding/json does.
func (g *schemaGen) fields(t reflect.Type, props map[string]any, required *[]any) {
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" && f.Type.Kind() == reflect.Struct {
			g.fields(f.Type, props, required)
			continue
		}
		if name == "" {
			name = f.Name
		}
		omit := strings.Contains(","+opts+",", ",omitempty,")
		var s map[string]any
		if vals, ok := schemaFieldEnums[t.Name()+"."+f.Name]; ok {
			s = map[string]any{"type": "string", "enum": vals}
		} else {
			s = g.of(f.Type, !omit)
		}
		props[name] = s
		if !omit {
			*required = append(*required, name)
		}
	}
}

// envelopeSchema is the schema of an envelope of kind whose summary is
// summaryType and whose data is dataType.
func envelopeSchema(kind Kind, title, description string, summaryType, dataType reflect.Type) envelopeDoc {
	g := &schemaGen{defs: map[string]any{}}
	sumProps, sumReq := map[string]any{}, []any{}
	for i := range summaryType.NumField() {
		name, _, _ := strings.Cut(summaryType.Field(i).Tag.Get("json"), ",")
		sumProps[name] = map[string]any{"type": "integer", "minimum": 0}
		sumReq = append(sumReq, name)
	}
	data := g.of(dataType, false)
	return envelopeDoc{
		Schema:      "https://json-schema.org/draft/2020-12/schema",
		ID:          "https://github.com/RhythmShahriar/echo/report/schema/" + string(kind) + ".schema.json",
		Title:       "rhythm-echo/v1 " + title,
		Description: description,
		Type:        "object",
		Required:    []string{"schema", "kind", "tool", "version", "complete", "summary", "verdict", "data"},
		Properties: map[string]any{
			"schema":   map[string]any{"const": model.Schema},
			"kind":     map[string]any{"const": string(kind)},
			"tool":     map[string]any{"const": "rhythm-echo"},
			"version":  map[string]any{"type": "string"},
			"complete": map[string]any{"type": "boolean"},
			"summary":  map[string]any{"type": "object", "required": sumReq, "properties": sumProps},
			"verdict":  map[string]any{"enum": []any{VerdictOK, VerdictFindings, VerdictError}},
			"data":     data,
		},
		Defs: g.defs,
	}
}

// envelopeDoc is a schema file's top level, in the order the other
// schemas give it ($schema first); a map would sort the keys.
type envelopeDoc struct {
	Schema      string         `json:"$schema"`
	ID          string         `json:"$id"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Type        string         `json:"type"`
	Required    []string       `json:"required"`
	Properties  map[string]any `json:"properties"`
	Defs        map[string]any `json:"$defs"`
}

// consentSchemas are the four consent schemas as the types say.
func consentSchemas() map[Kind]envelopeDoc {
	sum := reflect.TypeFor[Summary]()
	return map[Kind]envelopeDoc{
		KindConsent: envelopeSchema(KindConsent, "consent",
			"What `consent --json` writes; data is also consent.json. Generated from the Go types (report/consent_schema_test.go). New optional fields may be added without a new schema name.",
			reflect.TypeFor[ConsentSummary](), reflect.TypeFor[consent.Result]()),
		KindConsentCatalog: envelopeSchema(KindConsentCatalog, "consent-catalog",
			"What `consent cmps --json` and `consent trackers --json` write. Generated from the Go types (report/consent_schema_test.go).",
			sum, reflect.TypeFor[ConsentCatalog]()),
		KindConsentDetect: envelopeSchema(KindConsentDetect, "consent-detect",
			"What `consent detect --json` writes. Generated from the Go types (report/consent_schema_test.go).",
			sum, reflect.TypeFor[consent.DetectResult]()),
		KindConsentVerify: envelopeSchema(KindConsentVerify, "consent-verify",
			"What `consent verify --json` writes. Generated from the Go types (report/consent_schema_test.go).",
			sum, reflect.TypeFor[ConsentVerify]()),
	}
}

func TestConsentSchemas(t *testing.T) {
	for kind, s := range consentSchemas() {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		if err := enc.Encode(s); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join("schema", string(kind)+".schema.json")
		if *update {
			if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		got, err := Schema(kind)
		if err != nil {
			t.Fatal(err)
		}
		if *update {
			continue // the embedded copy is the old one until the next build
		}
		if !bytes.Equal(got, buf.Bytes()) {
			t.Errorf("%s no longer matches the Go types (go test ./report -run ConsentSchemas -update rewrites it)", path)
		}
	}
}

// Every typed string a consent result holds is listed, so a value added
// to the core types can't slip past the schema.
func TestSchemaEnumsComplete(t *testing.T) {
	if got := schemaEnums[reflect.TypeFor[consent.RunKind]()]; len(got) != 5 {
		t.Errorf("run kinds %v", got)
	}
	var walk func(t reflect.Type, seen map[reflect.Type]bool)
	walk = func(typ reflect.Type, seen map[reflect.Type]bool) {
		if seen[typ] {
			return
		}
		seen[typ] = true
		switch typ.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Map:
			walk(typ.Elem(), seen)
		case reflect.Struct:
			for i := range typ.NumField() {
				if f := typ.Field(i); f.Tag.Get("json") != "-" {
					walk(f.Type, seen)
				}
			}
		case reflect.String:
			if typ.Name() != "string" && schemaEnums[typ] == nil {
				t.Errorf("typed string %s has no enum in schemaEnums", typ)
			}
		}
	}
	for _, typ := range []reflect.Type{reflect.TypeFor[consent.Result](), reflect.TypeFor[consent.DetectResult](),
		reflect.TypeFor[ConsentCatalog](), reflect.TypeFor[ConsentVerify]()} {
		walk(typ, map[reflect.Type]bool{})
	}
}
