// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package config loads Echo's project settings from .echo/config.yaml:
// how the catcher behaves (seal or observe, what may be reached), what
// counts as staging and production, plus the actions `snapshot` and
// `compare` run. Every key is optional.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/echo/model"
)

// Dir is the project folder Echo keeps its settings, snapshots and runs in.
const Dir = ".echo"

// Config is .echo/config.yaml. Load fills the defaults in.
type Config struct {
	Site            string           `yaml:"site,omitempty"`             // base URL relative actions use, e.g. https://staging.shop.test
	Mode            string           `yaml:"mode,omitempty"`             // seal (default) or observe
	Listen          string           `yaml:"listen,omitempty"`           // API address, default 127.0.0.1:8025
	SMTP            string           `yaml:"smtp,omitempty"`             // SMTP sink address, default 127.0.0.1:1025; "off" disables
	Proxy           string           `yaml:"proxy,omitempty"`            // HTTP(S) proxy address, default off; e.g. 127.0.0.1:8026
	MITM            bool             `yaml:"mitm,omitempty"`             // decrypt HTTPS through the proxy (needs Echo's CA trusted)
	Allow           []string         `yaml:"allow,omitempty"`            // hosts reachable even when sealed: ["api.stripe.com", "*.sandbox.paypal.com"]
	AllowRecipients []string         `yaml:"allow_recipients,omitempty"` // addresses or numbers that may really be messaged in observe mode
	Staging         []string         `yaml:"staging,omitempty"`          // URL prefixes that are staging: replay only sends to these (and local addresses)
	Production      []string         `yaml:"production,omitempty"`       // live sites, as URLs or hosts (subdomains count): actions and replays refuse them
	Flush           []string         `yaml:"flush,omitempty"`            // commands run after each action so async senders send now
	Quiet           Duration         `yaml:"quiet,omitempty"`            // how long nothing must arrive before an action counts as done; default 2s, at most 30s and timeout
	Timeout         Duration         `yaml:"timeout,omitempty"`          // longest wait per action; default 30s, at most 1m
	Checks          map[string]Level `yaml:"checks,omitempty"`           // per check ID: off, notice, warning, error, broke or changed
	Rules           []Rule           `yaml:"rules,omitempty"`            // project checks: text that must never be sent
	Inbox           *Inbox           `yaml:"inbox,omitempty"`            // where `up` keeps captures; default in memory
	Relay           *Relay           `yaml:"relay,omitempty"`            // releasing a caught email to a real inbox, for client sign-off
	Hub             *Reserved        `yaml:"hub,omitempty"`              // reserved for the agency hub; accepted and not read yet
	Actions         []Action         `yaml:"actions,omitempty"`

	lines positions // where each key was in the file, for error messages
}

// Action is something that makes the site send messages. Exactly one of
// HTTP, Command or Replay is set.
type Action struct {
	Name     string            `yaml:"name,omitempty"`
	HTTP     *HTTPAction       `yaml:"http,omitempty"`
	Command  string            `yaml:"command,omitempty"` // run with sh -c (cmd /C on Windows); RHYTHM_ECHO_RUN is set
	Replay   *ReplayAction     `yaml:"replay,omitempty"`
	Env      map[string]string `yaml:"env,omitempty"`
	Expect   Expect            `yaml:"expect,omitempty"`   // optional: fail the action unless these were sent
	Ignore   []string          `yaml:"ignore,omitempty"`   // fields, headers or recipients compare leaves out (globs)
	Quiet    *Duration         `yaml:"quiet,omitempty"`    // overrides the global quiet for this action; same limits
	Parallel *bool             `yaml:"parallel,omitempty"` // false: never run beside another action; default true
}

type HTTPAction struct {
	Method  string            `yaml:"method,omitempty"`
	URL     string            `yaml:"url,omitempty"` // absolute, or relative to site
	Headers map[string]string `yaml:"headers,omitempty"`
	Form    map[string]string `yaml:"form,omitempty"` // sent as application/x-www-form-urlencoded
	JSON    any               `yaml:"json,omitempty"` // sent as application/json
	Body    string            `yaml:"body,omitempty"`
}

type ReplayAction struct {
	Event  string            `yaml:"event,omitempty"` // e.g. payment_intent.succeeded, or a path to a JSON file
	To     string            `yaml:"to,omitempty"`    // absolute, or relative to site
	Set    map[string]string `yaml:"set,omitempty"`   // field overrides: data.object.amount: 1999
	Secret string            `yaml:"secret_env,omitempty"`
}

// Expect lists what an action must send, by channel: a count
// (email: 2) or counts per event (analytics: {purchase: 1}), matched on
// the message's event.
type Expect map[model.Channel]Count

// Count is one channel's expectation: N messages of any kind, or Events
// by name.
type Count struct {
	N      int
	Events map[string]int
}

// Total is how many messages the expectation asks for.
func (c Count) Total() int {
	n := c.N
	for _, v := range c.Events {
		n += v
	}
	return n
}

// Total is how many messages every channel's expectation asks for.
func (e Expect) Total() int {
	n := 0
	for _, c := range e {
		n += c.Total()
	}
	return n
}

// UnmarshalYAML reads {email: 2, analytics: {purchase: 1}}, naming the
// line of an unknown channel or a bad count.
func (e *Expect) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: expect wants channels and counts, like {email: 2}", n.Line)
	}
	out := Expect{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		ch := model.Channel(k.Value)
		if !slices.Contains(model.Channels, ch) {
			return fmt.Errorf("line %d: expect: %q isn't a channel; use %s", k.Line, k.Value, channelList())
		}
		if _, dup := out[ch]; dup {
			return fmt.Errorf("line %d: expect: %s is listed twice", k.Line, k.Value)
		}
		var c Count
		switch v.Kind {
		case yaml.ScalarNode:
			if err := v.Decode(&c.N); err != nil || c.N < 0 {
				return fmt.Errorf("line %d: expect: %s wants a count like 1, or events like {purchase: 1}", v.Line, k.Value)
			}
		case yaml.MappingNode:
			c.Events = map[string]int{}
			for j := 0; j+1 < len(v.Content); j += 2 {
				ek, ev := v.Content[j], v.Content[j+1]
				var cnt int
				if err := ev.Decode(&cnt); err != nil || cnt < 0 || ek.Value == "" {
					return fmt.Errorf("line %d: expect: %s: %q wants a count like 1", ev.Line, k.Value, ek.Value)
				}
				c.Events[ek.Value] = cnt
			}
		default:
			return fmt.Errorf("line %d: expect: %s wants a count like 1, or events like {purchase: 1}", v.Line, k.Value)
		}
		out[ch] = c
	}
	*e = out
	return nil
}

// MarshalYAML writes the shape UnmarshalYAML reads.
func (c Count) MarshalYAML() (any, error) {
	if c.Events != nil {
		return c.Events, nil
	}
	return c.N, nil
}

func channelList() string {
	var names []string
	for _, c := range model.Channels {
		names = append(names, string(c))
	}
	return strings.Join(names, ", ")
}

// Level is how much a check counts, set per check ID under checks: an
// issue level (notice, warning, error), a comparison class (broke,
// changed) or off to drop it.
type Level string

const (
	Off     Level = "off"
	Notice  Level = "notice"
	Warning Level = "warning"
	Error   Level = "error"
	Broke   Level = "broke"
	Changed Level = "changed"
)

// Levels lists every level checks: accepts.
var Levels = []Level{Off, Notice, Warning, Error, Broke, Changed}

// ruleLevels are the levels a custom rule's issue can have.
var ruleLevels = []Level{Notice, Warning, Error}

// Rule is a project check: a message on one of Channels (all when empty)
// whose subject or visible text contains Match, or matches Regex, gets an
// issue with Level and Message. Match is literal and case-sensitive; use
// a regex with (?i) to ignore case.
type Rule struct {
	ID       string          `yaml:"id,omitempty"` // e.g. brand/old-phone
	Channels []model.Channel `yaml:"channels,omitempty"`
	Match    string          `yaml:"match,omitempty"`
	Regex    string          `yaml:"regex,omitempty"`
	Level    Level           `yaml:"level,omitempty"`   // notice, warning (default) or error
	Message  string          `yaml:"message,omitempty"` // the issue's title; default "Matched <id>"
	Fixtures *RuleFixtures   `yaml:"fixtures,omitempty"`
}

// RuleFixtures are texts a rule must and must not match, so a rule is
// tested like the built-in checks.
type RuleFixtures struct {
	Match   []string `yaml:"match,omitempty"`
	NoMatch []string `yaml:"no_match,omitempty"`
}

// Pattern compiles the rule's match or regex.
func (r Rule) Pattern() (*regexp.Regexp, error) {
	if r.Regex != "" {
		return regexp.Compile(r.Regex)
	}
	return regexp.Compile(regexp.QuoteMeta(r.Match))
}

// Inbox is where `up` keeps what it catches.
type Inbox struct {
	Store   string   `yaml:"store,omitempty"`    // "memory" (default) or a folder for a persistent inbox, e.g. .echo/inbox
	Keep    int      `yaml:"keep,omitempty"`     // most messages kept; 0 means the built-in default
	KeepFor Duration `yaml:"keep_for,omitempty"` // drop messages older than this, e.g. 14d; 0 keeps them
}

// Relay sends a caught email on to a real inbox, only to Allow.
type Relay struct {
	URL   string   `yaml:"url,omitempty"`   // smtp://user:pass@host:587 (or smtps://); the --relay flag can give it instead
	Allow []string `yaml:"allow,omitempty"` // recipient globs, e.g. ["*@client-a.com"]
}

// Reserved holds a key Echo accepts so configs written for a newer
// feature still load, though it doesn't read it yet.
type Reserved struct{ node yaml.Node }

// Node is the key's YAML as written.
func (r *Reserved) Node() *yaml.Node { return &r.node }

func (r *Reserved) UnmarshalYAML(n *yaml.Node) error {
	r.node = *n
	return nil
}

// MarshalYAML writes the key back as it was read.
func (r Reserved) MarshalYAML() (any, error) { return &r.node, nil }

// Duration reads "2s" style values as well as days ("14d").
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := parseDuration(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: %q isn't a duration like 2s", n.Line, n.Value)
	}
	d.Duration = v
	return nil
}

// MarshalYAML writes the duration the way UnmarshalYAML reads it.
func (d Duration) MarshalYAML() (any, error) { return d.String(), nil }

var days = regexp.MustCompile(`^(\d+)d(.*)$`)

// parseDuration is time.ParseDuration plus a leading day count ("14d",
// "1d12h"), since retention is set in days.
func parseDuration(s string) (time.Duration, error) {
	m := days.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return time.ParseDuration(s)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n > 100000 {
		return 0, fmt.Errorf("%q: too many days", s)
	}
	d := time.Duration(n) * 24 * time.Hour
	if m[2] != "" {
		rest, err := time.ParseDuration(m[2])
		if err != nil {
			return 0, err
		}
		d += rest
	}
	return d, nil
}

// Default returns the settings used when there's no config file.
func Default() Config {
	return Config{Mode: "seal", Listen: "127.0.0.1:8025", SMTP: "127.0.0.1:1025",
		Quiet: Duration{2 * time.Second}, Timeout: Duration{30 * time.Second}}
}

// Load reads path over the defaults. A missing file is not an error;
// unknown keys are, so typos fail loudly.
func Load(path string) (Config, error) {
	c := Default()
	data, err := os.ReadFile(path) //nolint:gosec // the user's own project config
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	c, err = parse(data)
	if err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// parse decodes and validates a config file's content, for Load and for
// SetAddresses, which checks a rewrite before it's written.
func parse(data []byte) (Config, error) {
	c := Default()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
		return c, err
	}
	var root yaml.Node
	if yaml.Unmarshal(data, &root) == nil {
		c.lines = index(&root)
	}
	return c, c.Validate()
}

// Validate checks values a user could get wrong. When c came from Load,
// errors name the line.
func (c Config) Validate() error {
	if c.Mode != "seal" && c.Mode != "observe" {
		return fmt.Errorf("%smode: %q isn't seal or observe", c.at("mode"), c.Mode)
	}
	for i, p := range c.Production {
		if entryURL(p) == nil {
			return fmt.Errorf("%sproduction: %q isn't a URL or host name", c.at("production", i), p)
		}
	}
	if err := c.waits(); err != nil {
		return err
	}
	ids := map[string]int{} // action ID to the index of the action that has it
	for i, a := range c.Actions {
		n := 0
		if a.HTTP != nil {
			n++
		}
		if a.Command != "" {
			n++
		}
		if a.Replay != nil {
			n++
		}
		at := c.at("actions", i)
		switch {
		case strings.TrimSpace(a.Name) == "":
			return fmt.Errorf("%sactions[%d]: needs a name", at, i)
		case n != 1:
			return fmt.Errorf("%saction %q: set exactly one of http, command or replay", at, a.Name)
		case ActionID(a.Name) == actionPrefix:
			return fmt.Errorf("%saction %q: needs a letter or digit from a to z or 0 to 9 in its name, so compare can tell it apart; add one, like %q", at, a.Name, a.Name+" 1")
		}
		if j, ok := ids[ActionID(a.Name)]; ok {
			first := c.Actions[j].Name
			if first == a.Name {
				return fmt.Errorf("%saction %q is listed twice%s", at, a.Name, c.also("actions", j))
			}
			// Compare matches actions by ID, so two actions sharing one
			// would swap messages between them.
			return fmt.Errorf("%saction %q and action %q%s both get the ID %s; rename one", at, a.Name, first, c.also("actions", j), ActionID(a.Name))
		}
		switch {
		case a.HTTP != nil && a.HTTP.URL == "":
			return fmt.Errorf("%saction %q: http needs a url", at, a.Name)
		case a.Replay != nil && (a.Replay.Event == "" || a.Replay.To == ""):
			return fmt.Errorf("%saction %q: replay needs event and to", at, a.Name)
		}
		if a.Quiet != nil {
			if err := c.quiet(a.Quiet.Duration); err != nil {
				return fmt.Errorf("%saction %q: %w", c.at("actions", i, "quiet"), a.Name, err)
			}
		}
		for j, g := range a.Ignore {
			if err := glob(g); err != nil {
				return fmt.Errorf("%saction %q: ignore: %w", c.at("actions", i, "ignore", j), a.Name, err)
			}
		}
		ids[ActionID(a.Name)] = i
	}
	for _, id := range slices.Sorted(maps.Keys(c.Checks)) {
		if l := c.Checks[id]; !slices.Contains(Levels, l) {
			return fmt.Errorf("%schecks: %s: %q isn't %s", c.at("checks", id), id, l, orList(Levels))
		}
	}
	rules := map[string]bool{}
	for i, r := range c.Rules {
		at := c.at("rules", i)
		switch {
		case strings.TrimSpace(r.ID) == "" || strings.ContainsAny(r.ID, " \t"):
			return fmt.Errorf("%srules[%d]: needs an id without spaces, like brand/old-phone", at, i)
		case rules[r.ID]:
			return fmt.Errorf("%srule %q is listed twice", at, r.ID)
		case (r.Match == "") == (r.Regex == ""):
			return fmt.Errorf("%srule %q: set exactly one of match or regex", at, r.ID)
		case r.Level != "" && !slices.Contains(ruleLevels, r.Level):
			return fmt.Errorf("%srule %q: level %q isn't %s", c.at("rules", i, "level"), r.ID, r.Level, orList(ruleLevels))
		}
		if _, err := r.Pattern(); err != nil {
			return fmt.Errorf("%srule %q: regex: %w", c.at("rules", i, "regex"), r.ID, err)
		}
		for j, ch := range r.Channels {
			if !slices.Contains(model.Channels, ch) {
				return fmt.Errorf("%srule %q: %q isn't a channel; use %s", c.at("rules", i, "channels", j), r.ID, ch, channelList())
			}
		}
		rules[r.ID] = true
	}
	if c.Inbox != nil {
		switch {
		case c.Inbox.Keep < 0:
			return fmt.Errorf("%sinbox: keep can't be negative", c.at("inbox", "keep"))
		case c.Inbox.KeepFor.Duration < 0:
			return fmt.Errorf("%sinbox: keep_for can't be negative", c.at("inbox", "keep_for"))
		}
	}
	if c.Relay != nil {
		if c.Relay.URL != "" {
			// The URL holds a password, so it's never quoted back.
			u, err := url.Parse(c.Relay.URL)
			if err != nil || (u.Scheme != "smtp" && u.Scheme != "smtps") || u.Hostname() == "" {
				return fmt.Errorf("%srelay: url wants smtp://user:pass@host:587 or smtps://…", c.at("relay", "url"))
			}
		}
		for j, g := range c.Relay.Allow {
			if err := glob(g); err != nil {
				return fmt.Errorf("%srelay: allow: %w", c.at("relay", "allow", j), err)
			}
		}
	}
	return nil
}

// The longest timeout and quiet Echo's wait API honors. It cuts longer
// values short without saying so, so Validate refuses them instead.
const (
	MaxTimeout = time.Minute
	MaxQuiet   = 30 * time.Second
)

// waits checks the global timeout and quiet: 0 < quiet <= timeout <=
// MaxTimeout with quiet <= MaxQuiet. A timeout of 0 failed every action
// at once and left the HTTP client waiting forever.
func (c Config) waits() error {
	t := c.Timeout.Duration
	switch {
	case t <= 0:
		return fmt.Errorf("%stimeout: %s must be more than 0, like 30s", c.at("timeout"), Short(t))
	case t > MaxTimeout:
		return fmt.Errorf("%stimeout: %s is over the %s Echo waits for one action at most", c.at("timeout"), Short(t), Short(MaxTimeout))
	}
	if err := c.quiet(c.Quiet.Duration); err != nil {
		return fmt.Errorf("%s%w", c.at("quiet"), err)
	}
	return nil
}

// quiet checks one quiet value, global or an action's, against the cap
// and the global timeout. The caller adds where it is.
func (c Config) quiet(q time.Duration) error {
	t := c.Timeout.Duration
	switch {
	case q <= 0:
		return fmt.Errorf("quiet: %s must be more than 0, like 2s", Short(q))
	case q > MaxQuiet:
		return fmt.Errorf("quiet: %s is over the %s Echo waits for quiet at most", Short(q), Short(MaxQuiet))
	case q > t:
		return fmt.Errorf("quiet: %s is longer than the timeout of %s%s, so no action could ever fall quiet", Short(q), Short(t), c.also("timeout"))
	}
	return nil
}

// Short writes a duration the way a config would: 1m, not 1m0s, and 1h,
// not 1h0m0s. Messages about waits use it so a value reads the same
// wherever it's reported.
func Short(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// glob checks an ignore or allow pattern: *, ? and [ ] as in path.Match.
func glob(g string) error {
	if strings.TrimSpace(g) == "" {
		return errors.New("an empty pattern matches nothing")
	}
	if _, err := path.Match(g, ""); err != nil {
		return fmt.Errorf("%q isn't a valid pattern (unclosed [ or a trailing \\)", g)
	}
	return nil
}

func orList(levels []Level) string {
	s := make([]string, len(levels))
	for i, l := range levels {
		s[i] = string(l)
	}
	return strings.Join(s[:len(s)-1], ", ") + " or " + s[len(s)-1]
}

// positions maps a key path (top-level key, then keys and list indexes)
// to its line in the file.
type positions map[string]int

func key(path ...any) string {
	parts := make([]string, len(path))
	for i, p := range path {
		parts[i] = fmt.Sprint(p)
	}
	return strings.Join(parts, "\x00")
}

// index records the line of every mapping key and list item under root.
func index(root *yaml.Node) positions {
	p := positions{}
	var walk func(n *yaml.Node, path []any)
	walk = func(n *yaml.Node, path []any) {
		switch n.Kind {
		case yaml.DocumentNode:
			for _, c := range n.Content {
				walk(c, path)
			}
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				sub := append(slices.Clip(path), any(n.Content[i].Value))
				p[key(sub...)] = n.Content[i].Line
				walk(n.Content[i+1], sub)
			}
		case yaml.SequenceNode:
			for i, c := range n.Content {
				sub := append(slices.Clip(path), any(i))
				p[key(sub...)] = c.Line
				walk(c, sub)
			}
		}
	}
	walk(root, nil)
	return p
}

// also names the line of an earlier entry in a message about a later
// one, as " (line 2)", or returns "".
func (c Config) also(path ...any) string {
	if n, ok := c.lines[key(path...)]; ok {
		return fmt.Sprintf(" (line %d)", n)
	}
	return ""
}

// ActionID is the stable ID compare matches an action on across runs:
// action: and the name in lower case, with every run of other characters
// turned into a dash, so "Contact form" is action:contact-form.
func ActionID(name string) string {
	return actionPrefix + strings.Trim(nonWord.ReplaceAllString(strings.ToLower(name), "-"), "-")
}

const actionPrefix = "action:"

var nonWord = regexp.MustCompile(`[^a-z0-9]+`)

// at returns "line N: " for the key at path, or "" when c didn't come
// from a file.
func (c Config) at(path ...any) string {
	if n, ok := c.lines[key(path...)]; ok {
		return fmt.Sprintf("line %d: ", n)
	}
	return ""
}

// IsProduction reports whether raw is a live site listed under
// production.
func (c Config) IsProduction(raw string) bool { return ProductionEntry(c.Production, raw) != "" }

// ProductionReason says why raw counts as a live site, for messages such
// as "refusing to run against <url>: <reason>", or returns "" when it
// doesn't.
func (c Config) ProductionReason(raw string) string {
	e := ProductionEntry(c.Production, raw)
	if e == "" {
		return ""
	}
	return fmt.Sprintf("it matches %q under production", e)
}

// ProductionEntry returns the entry of production that raw falls under,
// or "". Entries are URLs or bare host names. A match ignores the scheme
// and the case of the host, treats :80 and :443 as no port, covers
// subdomains (www.shop.example is under shop.example) and needs a
// listed port or path only when the entry names one. The comparison is
// deliberately loose: refusing a staging URL by mistake costs a config
// edit, while missing a live one sends real mail.
func ProductionEntry(production []string, raw string) string {
	t := entryURL(raw)
	if t == nil {
		return ""
	}
	for _, e := range production {
		p := entryURL(e)
		switch {
		case p == nil:
		case t.host != p.host && !strings.HasSuffix(t.host, "."+p.host):
		case p.port != "" && t.port != p.port:
		case p.path != "" && t.path != p.path && !strings.HasPrefix(t.path, p.path+"/"):
		default:
			return e
		}
	}
	return ""
}

type site struct{ host, port, path string }

// entryURL reads a production entry or a target into a comparable form,
// or returns nil when it has no host.
func entryURL(raw string) *site {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil
	}
	if !strings.Contains(s, "://") {
		s = "https://" + strings.TrimPrefix(s, "//")
	}
	u, err := url.Parse(s)
	if err != nil {
		return nil
	}
	host := strings.TrimSuffix(strings.TrimPrefix(strings.ToLower(u.Hostname()), "*."), ".")
	if host == "" {
		return nil
	}
	port := u.Port()
	if port == "80" || port == "443" {
		port = ""
	}
	return &site{host: host, port: port, path: strings.ToLower(strings.TrimSuffix(u.Path, "/"))}
}

// Path is the default config file location.
func Path() string { return filepath.Join(Dir, "config.yaml") }

// Template is what `rhythm echo init` writes. It must stay loadable.
const Template = `# Echo settings. Everything here is optional; delete what you don't change.
# Reference: https://github.com/RhythmShahriar/rhythm/wiki/Echo-Configuration
# Commit this file to share it; snapshots and runs are kept out of git.

site: ""                # base URL for relative action URLs, e.g. https://staging.shop.test
mode: seal              # seal: nothing leaves, Echo answers instead. observe: record and send for real
listen: 127.0.0.1:8025  # API and web view (connectors post here)
smtp: 127.0.0.1:1025    # SMTP sink; "off" to disable
proxy: ""               # HTTP(S) proxy for SDKs that skip the connector, e.g. 127.0.0.1:8026
mitm: false             # decrypt HTTPS through the proxy (trust the CA from: rhythm echo ca)

allow: []               # hosts reachable even when sealed, e.g. ["api.stripe.com"] (test-mode keys only)
allow_recipients: []    # in observe mode, recipients you may really message, e.g. ["*@yourcompany.com"]
staging: []             # URL prefixes that are staging; replay only sends to these and local addresses
production: []          # live sites, e.g. ["shop.com"] (subdomains too); actions and replays refuse them

flush: []               # run after each action so queued mail and webhooks go out now, e.g.
                        #   ["wp cron event run --due-now", "wp action-scheduler run"]
                        #   ["php artisan queue:work --stop-when-empty"]
quiet: 2s               # an action is done when nothing new arrives for this long (at most 30s)
timeout: 30s            # longest wait per action (at least quiet, at most 1m)

# What snapshot and compare run. Each action makes the site send something.
# Form submits and multi-step journeys are better run by Soundcheck with --echo.
actions: []
#  - name: Contact form
#    http: {method: POST, url: /wp-admin/admin-ajax.php, form: {action: contact, email: "${fake:email}", message: Hello}}
#    expect: {email: 2}
#  - name: Completed order
#    command: wp eval 'do_action("woocommerce_order_status_completed", 123);'
#    expect: {email: 1, analytics: {purchase: 1}}   # any channel: a count, or counts per event
#  - name: Stripe payment succeeded
#    replay: {event: payment_intent.succeeded, to: /?wc-api=wc_stripe, secret_env: ECHO_STRIPE_WEBHOOK_SECRET}
`
