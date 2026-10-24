// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package recognize

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/echo/model"
)

// Declarative providers: a provider whose API is one JSON call per
// message (a host, a path, where the recipient is) is described in
// providers/<provider>.yaml instead of Go, with its fixtures beside it in
// providers/<provider>.fixtures.yaml. The loader refuses a provider
// without at least one positive and one negative fixture. It also checks
// each one matches (or doesn't) before the provider is registered.
//
//	provider: mailjet              # slug stored on messages; the file name too
//	name: Mailjet                  # display name
//	channel: email                 # any channel but payment
//	host: api.mailjet.com          # one or a list; same patterns as Recognizer.Hosts
//	path: /v3.1/send               # one or a list; "*" stands for one segment
//	method: POST                   # optional; one or a list
//	to: $.Messages[*].To[*].Email  # fields: to, cc, bcc, from, reply_to,
//	subject: $.Messages[0].Subject #   subject, text, html, event
//	reply: {status: 200, body: {"Messages": [{"Status": "success"}]}}
//
// A field is one source or a list tried in order; the first that yields
// anything wins. Sources are JSONPath-lite on the JSON body ($, .key,
// [n], [*]), form:<field> (body or query string), query:<param>,
// header:<name> and path:<n> (a path segment from 0, or from the end when
// negative). to, cc and bcc keep every value, event joins them with ","
// and the other fields keep the first. The reply body is a string or YAML
// that is sent as JSON; ${id}, ${uuid} and ${now} in it are replaced by
// a fresh ID, a UUID and the time.
//
// Payment providers stay in Go (payment.go): a live call must always be
// refused. That needs code that tells live from test.

//go:embed providers
var providerFiles embed.FS

// declared holds the YAML providers with their fixtures, for the tests.
var declared []declaredProvider

func init() {
	ps, err := loadProviders(providerFiles, "providers")
	if err != nil {
		panic("recognize: " + err.Error())
	}
	declared = ps
	for _, p := range ps {
		register(p.rec)
	}
}

// declaredProvider is one loaded YAML provider.
type declaredProvider struct {
	file     string
	rec      Recognizer
	positive []fixture
	negative []fixture
}

// providerSpec is the YAML form of a provider.
type providerSpec struct {
	Provider string    `yaml:"provider"`
	Name     string    `yaml:"name"`
	Channel  string    `yaml:"channel"`
	Host     strList   `yaml:"host"`
	Path     strList   `yaml:"path"`
	Method   strList   `yaml:"method"`
	To       strList   `yaml:"to"`
	CC       strList   `yaml:"cc"`
	BCC      strList   `yaml:"bcc"`
	From     strList   `yaml:"from"`
	ReplyTo  strList   `yaml:"reply_to"`
	Subject  strList   `yaml:"subject"`
	Text     strList   `yaml:"text"`
	HTML     strList   `yaml:"html"`
	Event    strList   `yaml:"event"`
	Reply    replySpec `yaml:"reply"`
}

type replySpec struct {
	Status  int               `yaml:"status"`
	Type    string            `yaml:"type"`
	Headers map[string]string `yaml:"headers"`
	Body    yaml.Node         `yaml:"body"`
}

// fixtureFile is the YAML form of <provider>.fixtures.yaml.
type fixtureFile struct {
	Positive []fixture `yaml:"positive"`
	Negative []fixture `yaml:"negative"`
}

// fixture is one request plus, for a positive one, what it must fill in.
type fixture struct {
	Name    string            `yaml:"name"`
	Method  string            `yaml:"method"`
	URL     string            `yaml:"url"`
	Headers map[string]string `yaml:"headers"`
	Body    yaml.Node         `yaml:"body"`
	Want    map[string]any    `yaml:"want"`
	line    int
	req     Request
}

// strList is a YAML scalar or a list of them.
type strList []string

func (s *strList) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		*s = strList{n.Value}
		return nil
	case yaml.SequenceNode:
		var out []string
		for _, c := range n.Content {
			if c.Kind != yaml.ScalarNode {
				return fmt.Errorf("line %d: want a string", c.Line)
			}
			out = append(out, c.Value)
		}
		*s = out
		return nil
	}
	return fmt.Errorf("line %d: want a string or a list of strings", n.Line)
}

var (
	slugRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	yamlLine  = regexp.MustCompile(`^(?:yaml: )?line (\d+): (.*)$`)
	channels  = []model.Channel{model.Email, model.SMS, model.WhatsApp, model.Webhook, model.Chat, model.Analytics, model.Push, model.HTTP}
	methods   = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}
	wantField = []string{"channel", "to", "cc", "bcc", "from", "reply_to", "subject", "text", "html", "event"}
)

// loadProviders reads every provider in dir of fsys, in file-name order.
// Errors name the file and, where there is one, the line.
func loadProviders(fsys fs.FS, dir string) ([]declaredProvider, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	var out []declaredProvider
	seen := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".yaml") {
			continue
		}
		file := path.Join(dir, name)
		if base, ok := strings.CutSuffix(name, ".fixtures.yaml"); ok {
			if _, err := fs.Stat(fsys, path.Join(dir, base+".yaml")); err != nil {
				return nil, fmt.Errorf("%s: fixtures for a provider that doesn't exist (no %s.yaml)", file, base)
			}
			continue
		}
		p, err := loadProvider(fsys, file)
		if err != nil {
			return nil, err
		}
		if other, dup := seen[p.rec.Provider]; dup {
			return nil, fmt.Errorf("%s: provider %q is already defined in %s", file, p.rec.Provider, other)
		}
		seen[p.rec.Provider] = file
		out = append(out, p)
	}
	return out, nil
}

func loadProvider(fsys fs.FS, file string) (declaredProvider, error) {
	p := declaredProvider{file: file}
	b, err := fs.ReadFile(fsys, file)
	if err != nil {
		return p, err
	}
	var spec providerSpec
	root, err := decodeStrict(b, &spec)
	if err != nil {
		return p, located(file, err)
	}
	fail := func(key, format string, args ...any) error {
		return fmt.Errorf("%s:%d: %s", file, keyLine(root, key), fmt.Sprintf(format, args...))
	}
	base := strings.TrimSuffix(path.Base(file), ".yaml")
	switch {
	case !slugRe.MatchString(spec.Provider):
		return p, fail("provider", "provider %q must be lowercase letters, digits and dashes", spec.Provider)
	case spec.Provider != base:
		return p, fail("provider", "provider %q must match the file name (%s.yaml)", spec.Provider, base)
	case spec.Name == "":
		return p, fail("name", "name is required")
	case model.Channel(spec.Channel) == model.Payment:
		return p, fail("channel", "payment providers are written in Go (recognize/payment.go), so live calls are always refused")
	case !slices.Contains(channels, model.Channel(spec.Channel)):
		return p, fail("channel", "unknown channel %q", spec.Channel)
	case len(spec.Host) == 0:
		return p, fail("host", "host is required")
	case len(spec.Path) == 0:
		return p, fail("path", "path is required")
	case spec.Reply.Status != 0 && (spec.Reply.Status < 100 || spec.Reply.Status > 599):
		return p, fail("reply", "reply status %d is not an HTTP status", spec.Reply.Status)
	}
	for _, h := range spec.Host {
		if h == "" || strings.ContainsAny(h, "/ ") {
			return p, fail("host", "host %q is not a host name or pattern", h)
		}
	}
	for _, pt := range spec.Path {
		if _, err := path.Match(pt, "/"); err != nil || !strings.HasPrefix(pt, "/") {
			return p, fail("path", "path %q must start with / (and \"*\" stands for one segment)", pt)
		}
	}
	for i, m := range spec.Method {
		spec.Method[i] = strings.ToUpper(m)
		if !slices.Contains(methods, spec.Method[i]) {
			return p, fail("method", "unknown method %q", m)
		}
	}
	fields := map[string]strList{"to": spec.To, "cc": spec.CC, "bcc": spec.BCC, "from": spec.From, "reply_to": spec.ReplyTo,
		"subject": spec.Subject, "text": spec.Text, "html": spec.HTML, "event": spec.Event}
	srcs := map[string][]source{}
	for key, list := range fields {
		for _, s := range list {
			src, err := parseSource(s)
			if err != nil {
				return p, fail(key, "%s: %v", key, err)
			}
			srcs[key] = append(srcs[key], src)
		}
	}
	reply, err := replyOf(spec.Reply)
	if err != nil {
		return p, fail("reply", "%v", err)
	}
	p.rec = declaredRecognizer(spec, srcs, reply)

	fixFile := strings.TrimSuffix(file, ".yaml") + ".fixtures.yaml"
	fb, err := fs.ReadFile(fsys, fixFile)
	if errors.Is(err, fs.ErrNotExist) {
		return p, fmt.Errorf("%s: no fixtures: add %s with at least one positive and one negative request", file, path.Base(fixFile))
	} else if err != nil {
		return p, err
	}
	var ff fixtureFile
	froot, err := decodeStrict(fb, &ff)
	if err != nil {
		return p, located(fixFile, err)
	}
	if len(ff.Positive) == 0 || len(ff.Negative) == 0 {
		return p, fmt.Errorf("%s:%d: %s needs at least one positive and one negative fixture (has %d and %d)",
			fixFile, keyLine(froot, "positive"), spec.Provider, len(ff.Positive), len(ff.Negative))
	}
	lines := fixtureLines(froot)
	for i := range ff.Positive {
		ff.Positive[i].line = lines["positive"][i]
	}
	for i := range ff.Negative {
		ff.Negative[i].line = lines["negative"][i]
	}
	for _, list := range [][]fixture{ff.Positive, ff.Negative} {
		for i := range list {
			f := &list[i]
			if err := f.build(); err != nil {
				return p, fmt.Errorf("%s:%d: %w", fixFile, f.line, err)
			}
			for k := range f.Want {
				if !slices.Contains(wantField, k) {
					return p, fmt.Errorf("%s:%d: want: unknown field %q", fixFile, f.line, k)
				}
			}
		}
	}
	for _, f := range ff.Positive {
		if !p.rec.Match(f.req) {
			return p, fmt.Errorf("%s:%d: positive fixture %q doesn't match %s", fixFile, f.line, f.Name, spec.Provider)
		}
	}
	for _, f := range ff.Negative {
		if p.rec.Match(f.req) {
			return p, fmt.Errorf("%s:%d: negative fixture %q matches %s", fixFile, f.line, f.Name, spec.Provider)
		}
	}
	p.positive, p.negative = ff.Positive, ff.Negative
	return p, nil
}

// decodeStrict decodes b into v and refuses unknown keys. It returns the
// document node for finding lines.
func decodeStrict(b []byte, v any) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		return nil, err
	}
	return &doc, nil
}

// located turns a YAML error into "file:line: message" lines.
func located(file string, err error) error {
	var te *yaml.TypeError
	msgs := []string{err.Error()}
	if errors.As(err, &te) {
		msgs = te.Errors
	}
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		if g := yamlLine.FindStringSubmatch(m); g != nil {
			out = append(out, file+":"+g[1]+": "+g[2])
		} else {
			out = append(out, file+": "+strings.TrimPrefix(m, "yaml: "))
		}
	}
	return errors.New(strings.Join(out, "\n"))
}

// keyLine returns the line of a top-level key, or 1.
func keyLine(doc *yaml.Node, key string) int {
	if doc == nil || len(doc.Content) == 0 {
		return 1
	}
	m := doc.Content[0]
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i].Line
		}
	}
	return m.Line
}

// fixtureLines returns the line of each fixture under positive and
// negative.
func fixtureLines(doc *yaml.Node) map[string][]int {
	out := map[string][]int{}
	if len(doc.Content) == 0 {
		return out
	}
	m := doc.Content[0]
	for i := 0; i+1 < len(m.Content); i += 2 {
		for _, c := range m.Content[i+1].Content {
			out[m.Content[i].Value] = append(out[m.Content[i].Value], c.Line)
		}
	}
	return out
}

// build turns the fixture into a Request. A body written as YAML (not a
// string) is sent as JSON.
func (f *fixture) build() error {
	if f.Name == "" {
		return errors.New("fixture needs a name")
	}
	u, err := url.Parse(f.URL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("fixture %q: url %q is not an absolute URL", f.Name, f.URL)
	}
	h := map[string]string{}
	for k, v := range f.Headers {
		h[strings.ToLower(k)] = v
	}
	var body []byte
	switch f.Body.Kind {
	case 0:
	case yaml.ScalarNode:
		body = []byte(f.Body.Value)
	default:
		var v any
		if err := f.Body.Decode(&v); err != nil {
			return fmt.Errorf("fixture %q: body: %w", f.Name, err)
		}
		body = []byte(js(v))
		if h["content-type"] == "" {
			h["content-type"] = "application/json"
		}
	}
	method := strings.ToUpper(f.Method)
	if method == "" {
		method = "POST"
	}
	f.req = Request{Method: method, URL: u, Headers: h, Body: body}
	return nil
}

// replyOf renders the reply's body template and content type.
func replyOf(r replySpec) (Fake, error) {
	f := Fake{Status: r.Status, Headers: map[string]string{}}
	if f.Status == 0 {
		f.Status = 200
	}
	for k, v := range r.Headers {
		f.Headers[strings.ToLower(k)] = v
	}
	switch r.Body.Kind {
	case 0:
	case yaml.ScalarNode:
		f.Body = r.Body.Value
		f.Headers["content-type"] = "text/plain; charset=utf-8"
	default:
		var v any
		if err := r.Body.Decode(&v); err != nil {
			return f, err
		}
		f.Body = js(v)
		f.Headers["content-type"] = "application/json"
	}
	if r.Type != "" {
		f.Headers["content-type"] = r.Type
	}
	if strings.Contains(f.Headers["content-type"], "json") && !json.Valid([]byte(f.Body)) {
		return f, fmt.Errorf("reply body is not JSON: %s", f.Body)
	}
	if f.Body == "" && len(f.Headers) == 0 {
		f.Headers = nil
	}
	return f, nil
}

// expand fills ${id}, ${uuid} and ${now} with fresh values.
func expand(s string) string {
	if !strings.Contains(s, "${") {
		return s
	}
	for _, p := range []struct {
		key string
		val func() string
	}{{"${id}", func() string { return randHex(24) }}, {"${uuid}", uuid}, {"${now}", now}} {
		for strings.Contains(s, p.key) {
			s = strings.Replace(s, p.key, p.val(), 1)
		}
	}
	return s
}

func declaredRecognizer(spec providerSpec, srcs map[string][]source, reply Fake) Recognizer {
	return Recognizer{
		Provider: spec.Provider,
		Name:     spec.Name,
		Channel:  model.Channel(spec.Channel),
		Hosts:    spec.Host,
		Match: func(r Request) bool {
			if len(spec.Method) > 0 && !slices.Contains(spec.Method, r.Method) {
				return false
			}
			if !slices.ContainsFunc(spec.Host, func(h string) bool { return hostCovered(h, r.URL) }) {
				return false
			}
			return slices.ContainsFunc(spec.Path, func(p string) bool {
				ok, _ := path.Match(p, r.URL.Path)
				return ok
			})
		},
		Fill: func(r Request, m *model.Message) {
			var body any
			if !r.JSON(&body) {
				body = nil
			}
			get := func(key string) []string {
				for _, s := range srcs[key] {
					if v := s.eval(r, body); len(v) > 0 {
						return v
					}
				}
				return nil
			}
			one := func(key string) string { return first(get(key)) }
			m.To, m.CC, m.BCC = get("to"), get("cc"), get("bcc")
			m.From, m.ReplyTo, m.Subject, m.Text, m.HTML = one("from"), one("reply_to"), one("subject"), one("text"), one("html")
			m.Event = strings.Join(get("event"), ",")
		},
		Fake: func(Request, *model.Message) Fake {
			f := reply
			f.Body = expand(reply.Body)
			return f
		},
	}
}

// hostCovered reports whether u's host is covered by a Hosts pattern.
func hostCovered(pattern string, u *url.URL) bool {
	pattern = strings.ToLower(pattern)
	if strings.Contains(pattern, ":") {
		ok, _ := path.Match(pattern, strings.ToLower(u.Host))
		return ok
	}
	h := strings.ToLower(u.Hostname())
	if strings.Contains(pattern, "*") {
		ok, _ := path.Match(pattern, h)
		return ok
	}
	return h == pattern || strings.HasSuffix(h, "."+pattern)
}

// source is one place a field's value comes from.
type source struct {
	kind  string // json, form, query, header, path
	name  string // field, parameter or header name
	index int    // path segment
	steps []step // JSONPath-lite steps
}

// step is one JSONPath-lite step: a key, an index, or every element.
type step struct {
	key   string
	index int
	all   bool
}

func parseSource(s string) (source, error) {
	if strings.HasPrefix(s, "$") {
		steps, err := parseJSONPath(s)
		return source{kind: "json", steps: steps}, err
	}
	kind, name, ok := strings.Cut(s, ":")
	if !ok || name == "" {
		return source{}, fmt.Errorf("%q: want $.json.path, form:<field>, query:<param>, header:<name> or path:<n>", s)
	}
	switch kind {
	case "form", "query", "header":
		return source{kind: kind, name: name}, nil
	case "path":
		n, err := strconv.Atoi(name)
		if err != nil {
			return source{}, fmt.Errorf("%q: path wants a segment number", s)
		}
		return source{kind: kind, index: n}, nil
	}
	return source{}, fmt.Errorf("%q: unknown source %q", s, kind)
}

// parseJSONPath parses $ followed by .key, [n] and [*] steps.
func parseJSONPath(s string) ([]step, error) {
	rest := s[1:]
	var out []step
	for rest != "" {
		switch rest[0] {
		case '.':
			end := strings.IndexAny(rest[1:], ".[")
			if end < 0 {
				end = len(rest) - 1
			}
			key := rest[1 : end+1]
			if key == "" {
				return nil, fmt.Errorf("%q: empty key", s)
			}
			out, rest = append(out, step{key: key}), rest[end+1:]
		case '[':
			end := strings.IndexByte(rest, ']')
			if end < 0 {
				return nil, fmt.Errorf("%q: unclosed [", s)
			}
			in := rest[1:end]
			if in == "*" {
				out = append(out, step{all: true})
			} else if n, err := strconv.Atoi(in); err == nil && n >= 0 {
				out = append(out, step{index: n})
			} else {
				return nil, fmt.Errorf("%q: [%s] wants a number or *", s, in)
			}
			rest = rest[end+1:]
		default:
			return nil, fmt.Errorf("%q: want . or [ after %q", s, s[:len(s)-len(rest)])
		}
	}
	return out, nil
}

// eval returns the source's values for r (body is r's decoded JSON).
func (s source) eval(r Request, body any) []string {
	switch s.kind {
	case "json":
		vals := []any{body}
		for _, st := range s.steps {
			var next []any
			for _, v := range vals {
				switch {
				case st.key != "":
					if o, ok := v.(map[string]any); ok {
						if c, ok := o[st.key]; ok {
							next = append(next, c)
						}
					}
				case st.all:
					if a, ok := v.([]any); ok {
						next = append(next, a...)
					}
				default:
					if a, ok := v.([]any); ok && st.index < len(a) {
						next = append(next, a[st.index])
					}
				}
			}
			vals = next
		}
		var out []string
		for _, v := range vals {
			out = appendScalars(out, v)
		}
		return out
	case "form":
		return values(r)[s.name]
	case "query":
		return r.URL.Query()[s.name]
	case "header":
		if v := r.Header(s.name); v != "" {
			return []string{v}
		}
	case "path":
		segs := segments(r)
		i := s.index
		if i < 0 {
			i += len(segs)
		}
		if i >= 0 && i < len(segs) {
			return []string{segs[i]}
		}
	}
	return nil
}

// appendScalars appends v's strings and numbers (also those in a list)
// to out. Unlike strs it never splits a string at commas: a text field
// is one value.
func appendScalars(out []string, v any) []string {
	switch t := v.(type) {
	case []any:
		for _, e := range t {
			out = appendScalars(out, e)
		}
	case bool:
		out = append(out, strconv.FormatBool(t))
	default:
		if s := str(t); s != "" {
			out = append(out, s)
		}
	}
	return out
}
