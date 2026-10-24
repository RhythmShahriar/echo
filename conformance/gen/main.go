// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Command gen writes wire/schema.json, Echo's wire protocol as a JSON
// Schema, from the Go types in package wire. It runs from wire/ through
// go generate:
//
//	//go:generate go run ../conformance/gen -src wire.go -o schema.json
//
// The structure comes from reflection on the types: fields without
// omitempty are required, an enum tag lists a field's values and a since
// tag records the wire version that added it (x-since). The descriptions
// come from the comments in -src. gen_test.go fails when schema.json and
// the types drift apart.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"

	"github.com/RhythmShahriar/echo/wire"
)

// roots are the documents the protocol exchanges, in schema order.
var roots = []reflect.Type{reflect.TypeFor[wire.Capture](), reflect.TypeFor[wire.Decision]()}

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("gen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	src := fs.String("src", "wire.go", "the Go file with the wire types, for their comments")
	out := fs.String("o", "schema.json", "where to write the schema")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	b, err := generate(*src)
	if err == nil {
		err = os.WriteFile(*out, b, 0o644) //nolint:gosec // a source file, read by everyone
	}
	if err != nil {
		fmt.Fprintln(stderr, "gen:", err)
		return 1
	}
	return 0
}

// object is a JSON object whose keys keep the order they were set in, so
// the schema reads in the order of the Go source.
type object struct {
	keys []string
	vals map[string]any
}

func newObject() *object { return &object{vals: map[string]any{}} }

func (o *object) set(k string, v any) *object {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
	return o
}

// MarshalJSON writes the keys in order.
func (o *object) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		b.Write(kb)
		b.WriteByte(':')
		vb, err := json.Marshal(o.vals[k])
		if err != nil {
			return nil, err
		}
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// generate returns schema.json for the wire types, with the comments in
// the Go file src as descriptions.
func generate(src string) ([]byte, error) {
	docs, pkgDoc, err := comments(src)
	if err != nil {
		return nil, err
	}
	defs := newObject()
	var add func(t reflect.Type) error
	add = func(t reflect.Type) error {
		if _, done := defs.vals[t.Name()]; done {
			return nil
		}
		defs.set(t.Name(), nil) // reserve the slot: definitions follow first use
		def, nested, err := structSchema(t, docs)
		if err != nil {
			return err
		}
		defs.set(t.Name(), def)
		for _, n := range nested {
			if err := add(n); err != nil {
				return err
			}
		}
		return nil
	}
	alts := make([]any, 0, len(roots))
	for _, t := range roots {
		if err := add(t); err != nil {
			return nil, err
		}
		alts = append(alts, newObject().set("$ref", "#/$defs/"+t.Name()))
	}
	s := newObject().
		set("$schema", "https://json-schema.org/draft/2020-12/schema").
		set("$id", "https://github.com/RhythmShahriar/echo/blob/master/wire/schema.json").
		set("title", "Rhythm Echo wire protocol").
		set("description", pkgDoc+" Generated from wire/wire.go by conformance/gen: do not edit.").
		set("x-version", wire.Version).
		set("x-min-version", wire.MinVersion).
		set("anyOf", alts).
		set("$defs", defs)
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// structSchema describes one struct type. It also returns the struct types
// its fields use.
func structSchema(t reflect.Type, docs map[string]string) (*object, []reflect.Type, error) {
	props := newObject()
	var required []string
	var nested []reflect.Type
	for i := range t.NumField() {
		f := t.Field(i)
		name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" || !f.IsExported() {
			continue
		}
		p, inner, err := fieldSchema(f.Type)
		if err != nil {
			return nil, nil, fmt.Errorf("%s.%s: %w", t.Name(), f.Name, err)
		}
		if inner != nil {
			nested = append(nested, inner)
		}
		if d := docs[t.Name()+"."+f.Name]; d != "" {
			p.set("description", d)
		}
		if e := f.Tag.Get("enum"); e != "" {
			p.set("enum", strings.Split(e, ","))
		}
		if t == reflect.TypeFor[wire.Capture]() && name == "v" {
			p.set("minimum", wire.MinVersion).set("maximum", wire.Version)
		}
		if since := f.Tag.Get("since"); since != "" {
			n, err := strconv.Atoi(since)
			if err != nil {
				return nil, nil, fmt.Errorf("%s.%s: since %q is not a number", t.Name(), f.Name, since)
			}
			p.set("x-since", n)
		}
		props.set(name, p)
		if !strings.Contains(opts, "omitempty") {
			required = append(required, name)
		}
	}
	o := newObject().set("type", "object")
	if d := docs[t.Name()]; d != "" {
		o.set("description", d)
	}
	o.set("properties", props)
	if len(required) > 0 {
		o.set("required", required)
	}
	o.set("additionalProperties", false)
	return o, nested, nil
}

// fieldSchema describes a field's Go type. It also returns the struct type
// it refers to, if any.
func fieldSchema(t reflect.Type) (*object, reflect.Type, error) {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return newObject().set("type", "string"), nil, nil
	case reflect.Int, reflect.Int64:
		return newObject().set("type", "integer"), nil, nil
	case reflect.Bool:
		return newObject().set("type", "boolean"), nil, nil
	case reflect.Struct:
		return newObject().set("$ref", "#/$defs/"+t.Name()), t, nil
	case reflect.Slice:
		items, inner, err := fieldSchema(t.Elem())
		if err != nil {
			return nil, nil, err
		}
		return newObject().set("type", "array").set("items", items), inner, nil
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return nil, nil, fmt.Errorf("map keys must be strings, not %s", t.Key())
		}
		values, inner, err := fieldSchema(t.Elem())
		if err != nil {
			return nil, nil, err
		}
		return newObject().set("type", "object").set("additionalProperties", values), inner, nil
	}
	return nil, nil, fmt.Errorf("no JSON Schema for %s", t)
}

// comments reads the doc and line comments of the types and fields in the
// Go file src, keyed "Type" and "Type.Field", plus the package's synopsis.
func comments(src string) (map[string]string, string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, src, nil, parser.ParseComments)
	if err != nil {
		return nil, "", err
	}
	out := map[string]string{}
	for _, d := range f.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || g.Tok != token.TYPE {
			continue
		}
		for _, spec := range g.Specs {
			ts := spec.(*ast.TypeSpec)
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			typeDoc := ts.Doc
			if typeDoc == nil {
				typeDoc = g.Doc
			}
			out[ts.Name.Name] = text(typeDoc)
			for _, field := range st.Fields.List {
				c := text(field.Doc)
				if line := text(field.Comment); line != "" {
					c = strings.TrimSpace(c + " " + line)
				}
				for _, n := range field.Names {
					out[ts.Name.Name+"."+n.Name] = c
				}
			}
		}
	}
	var p doc.Package
	return out, p.Synopsis(text(f.Doc)), nil
}

// text is a comment as one line of plain text.
func text(g *ast.CommentGroup) string {
	if g == nil {
		return ""
	}
	return strings.Join(strings.Fields(g.Text()), " ")
}
