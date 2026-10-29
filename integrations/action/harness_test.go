// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package action

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode"

	"gopkg.in/yaml.v3"
)

// This file runs action.yml's composite steps the way a GitHub runner
// does, closely enough to test their bash locally: inputs and their
// defaults, ${{ }} expressions, if: conditions, step outputs, GITHUB_ENV,
// GITHUB_PATH, the job summary and working-directory. Steps that use
// another action are recorded, not run. Unknown inputs and contexts fail
// the test, so a typo in action.yml (a step ID, an input name) shows up
// here rather than in a user's workflow.

type actionFile struct {
	Inputs  map[string]struct{ Default string } `yaml:"inputs"`
	Outputs map[string]struct{ Value string }   `yaml:"outputs"`
	Runs    struct {
		Steps []step `yaml:"steps"`
	} `yaml:"runs"`
}

type step struct {
	Name             string            `yaml:"name"`
	ID               string            `yaml:"id"`
	If               string            `yaml:"if"`
	Uses             string            `yaml:"uses"`
	With             map[string]string `yaml:"with"`
	Run              string            `yaml:"run"`
	Shell            string            `yaml:"shell"`
	WorkingDirectory string            `yaml:"working-directory"`
	Env              map[string]string `yaml:"env"`
}

// runner is one job: the workspace, RUNNER_TEMP and what earlier steps
// left in GITHUB_ENV and GITHUB_PATH. Each use is one `uses: ./` step.
type runner struct {
	t         *testing.T
	action    actionFile
	workspace string
	temp      string
	github    map[string]string // github context, by path after "github."
	env       map[string]string // GITHUB_ENV so far
	path      []string          // GITHUB_PATH so far plus the test's own bin
	skip      []string          // step names not to run (the release download)
}

// result is what one use of the Action did.
type result struct {
	ok      bool              // every step succeeded
	outputs map[string]string // the Action's outputs
	ran     []string          // steps that ran, by name; uses: steps are listed, not run
	summary string            // the job summary the steps wrote
	log     string            // the steps' stdout and stderr
}

func newRunner(t *testing.T) *runner {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root(t), "action.yml"))
	if err != nil {
		t.Fatal(err)
	}
	r := &runner{t: t, workspace: realTempDir(t), temp: realTempDir(t), env: map[string]string{}}
	if err := yaml.Unmarshal(b, &r.action); err != nil {
		t.Fatal(err)
	}
	r.github = map[string]string{
		"action_path": root(t), "action_ref": "", "token": "test-token", "event_name": "push",
		"event.repository.private": "true", "event.pull_request.number": "",
		"repository": "RhythmShahriar/echo", "run_id": "42", "server_url": "https://github.com", "job": "test",
	}
	return r
}

// use runs the Action with inputs, as a workflow step would.
func (r *runner) use(inputs map[string]string) result {
	t := r.t
	t.Helper()
	ctx := &exprCtx{inputs: map[string]string{}, steps: map[string]map[string]string{}, github: r.github, temp: r.temp}
	for name, in := range r.action.Inputs {
		ctx.inputs[name] = in.Default
	}
	for name, v := range inputs {
		if _, ok := r.action.Inputs[name]; !ok {
			t.Fatalf("action.yml has no input %q", name)
		}
		ctx.inputs[name] = v
	}
	for _, s := range r.action.Runs.Steps {
		if s.ID != "" {
			ctx.steps[s.ID] = nil
		}
	}
	res := result{ok: true}
	var summary, log strings.Builder
	for _, s := range r.action.Runs.Steps {
		ctx.failed = !res.ok
		cond := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(s.If), "${{"), "}}")
		// GitHub adds success() to a condition without a status function.
		switch {
		case strings.TrimSpace(cond) == "":
			cond = "success()"
		case !statusFunc.MatchString(cond):
			cond = "success() && (" + cond + ")"
		}
		if !truthy(ctx.eval(t, cond)) {
			continue
		}
		res.ran = append(res.ran, s.Name)
		if s.Uses != "" || slices.Contains(r.skip, s.Name) {
			continue
		}
		out, sum, ok := r.runStep(ctx, s)
		summary.WriteString(sum)
		fmt.Fprintf(&log, "--- %s\n%s", s.Name, out)
		if !ok {
			res.ok = false
		}
	}
	res.outputs = map[string]string{}
	for name, o := range r.action.Outputs {
		res.outputs[name] = str(ctx.interpolate(t, o.Value))
	}
	res.summary, res.log = summary.String(), log.String()
	return res
}

// runStep runs one bash step and returns its output, its job summary and
// whether it succeeded.
func (r *runner) runStep(ctx *exprCtx, s step) (string, string, bool) {
	t := r.t
	t.Helper()
	if s.Shell != "bash" {
		t.Fatalf("step %q: shell %q, want bash", s.Name, s.Shell)
	}
	dir := t.TempDir()
	files := map[string]string{}
	for _, f := range []string{"GITHUB_OUTPUT", "GITHUB_ENV", "GITHUB_PATH", "GITHUB_STEP_SUMMARY"} {
		files[f] = filepath.Join(dir, f)
		if err := os.WriteFile(files[f], nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	script := filepath.Join(dir, "step.sh")
	if err := os.WriteFile(script, []byte(str(ctx.interpolate(t, s.Run))), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "--noprofile", "--norc", "-eo", "pipefail", script) //nolint:gosec // action.yml's own step
	cmd.Dir = r.workspace
	if s.WorkingDirectory != "" {
		cmd.Dir = filepath.Join(r.workspace, str(ctx.interpolate(t, s.WorkingDirectory)))
	}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GITHUB_") && !strings.HasPrefix(kv, "RHYTHM_ECHO_") && !strings.HasPrefix(kv, "PATH=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env,
		"PATH="+strings.Join(append(slices.Clone(r.path), os.Getenv("PATH")), string(os.PathListSeparator)),
		"RUNNER_TEMP="+r.temp, "GITHUB_WORKSPACE="+r.workspace, "GITHUB_REPOSITORY="+r.github["repository"],
		"GITHUB_RUN_ID="+r.github["run_id"], "GITHUB_SERVER_URL="+r.github["server_url"], "GITHUB_EVENT_NAME="+r.github["event_name"])
	for k, v := range files {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	for k, v := range r.env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	for k, v := range s.Env {
		cmd.Env = append(cmd.Env, k+"="+str(ctx.interpolate(t, v)))
	}
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatalf("step %q: %v", s.Name, err)
	}
	if s.ID != "" {
		ctx.steps[s.ID] = keyValues(t, files["GITHUB_OUTPUT"])
	}
	for k, v := range keyValues(t, files["GITHUB_ENV"]) {
		r.env[k] = v
	}
	if b, _ := os.ReadFile(files["GITHUB_PATH"]); len(b) > 0 {
		r.path = append(strings.Fields(string(b)), r.path...)
	}
	sum, _ := os.ReadFile(files["GITHUB_STEP_SUMMARY"])
	return string(out), string(sum), err == nil
}

// keyValues reads a GITHUB_OUTPUT or GITHUB_ENV file: name=value lines and
// name<<DELIMITER blocks.
func keyValues(t *testing.T, path string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // the step's own file
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := sc.Text()
		if k, delim, ok := strings.Cut(line, "<<"); ok && !strings.Contains(k, "=") {
			var v []string
			for sc.Scan() && sc.Text() != delim {
				v = append(v, sc.Text())
			}
			m[k] = strings.Join(v, "\n")
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			m[k] = v
		}
	}
	return m
}

// exprCtx evaluates the subset of GitHub's expression language action.yml
// uses: contexts, string literals, ==, !=, !, &&, ||, parentheses and the
// status functions.
type exprCtx struct {
	inputs map[string]string
	steps  map[string]map[string]string // nil: the step hasn't run
	github map[string]string
	temp   string
	failed bool
}

// statusFunc finds a status function in an if: condition.
var statusFunc = regexp.MustCompile(`\b(always|success|failure|cancelled)\(\)`) //nolint:misspell // the function GitHub names

var interp = regexp.MustCompile(`\$\{\{\s*(.*?)\s*\}\}`)

func (c *exprCtx) interpolate(t *testing.T, s string) any {
	t.Helper()
	if m := interp.FindStringSubmatch(s); m != nil && m[0] == strings.TrimSpace(s) {
		return c.eval(t, m[1])
	}
	return interp.ReplaceAllStringFunc(s, func(e string) string {
		return str(c.eval(t, interp.FindStringSubmatch(e)[1]))
	})
}

func (c *exprCtx) eval(t *testing.T, src string) any {
	t.Helper()
	p := &parser{t: t, c: c, toks: tokenize(t, src), src: src}
	v := p.or()
	if p.i != len(p.toks) {
		t.Fatalf("expression %q: unexpected %q", src, p.toks[p.i])
	}
	return v
}

func (c *exprCtx) lookup(t *testing.T, path string) any {
	t.Helper()
	parts := strings.Split(path, ".")
	switch {
	case parts[0] == "inputs" && len(parts) == 2:
		v, ok := c.inputs[parts[1]]
		if !ok {
			t.Fatalf("no input %q", parts[1])
		}
		return v
	case parts[0] == "steps" && len(parts) == 4 && parts[2] == "outputs":
		out, ok := c.steps[parts[1]]
		if !ok {
			t.Fatalf("no step with id %q", parts[1])
		}
		if v, ok := out[parts[3]]; ok {
			return v
		}
		return nil
	case path == "runner.temp":
		return c.temp
	case parts[0] == "github":
		v, ok := c.github[strings.TrimPrefix(path, "github.")]
		if !ok {
			t.Fatalf("the test doesn't know %s", path)
		}
		if v == "" {
			return nil
		}
		return v
	}
	t.Fatalf("unknown context %s", path)
	return nil
}

func tokenize(t *testing.T, s string) []string {
	t.Helper()
	var toks []string
	for i := 0; i < len(s); {
		switch ch := s[i]; {
		case ch == ' ':
			i++
		case ch == '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				t.Fatalf("unterminated string in %q", s)
			}
			toks = append(toks, s[i:i+j+2])
			i += j + 2
		case strings.HasPrefix(s[i:], "&&"), strings.HasPrefix(s[i:], "||"), strings.HasPrefix(s[i:], "=="), strings.HasPrefix(s[i:], "!="):
			toks = append(toks, s[i:i+2])
			i += 2
		case ch == '!' || ch == '(' || ch == ')':
			toks = append(toks, s[i:i+1])
			i++
		default:
			j := i
			for j < len(s) && (unicode.IsLetter(rune(s[j])) || unicode.IsDigit(rune(s[j])) || strings.ContainsRune("._-", rune(s[j]))) {
				j++
			}
			if j == i {
				t.Fatalf("unexpected %q in %q", s[i:], s)
			}
			toks = append(toks, s[i:j])
			i = j
		}
	}
	return toks
}

type parser struct {
	t    *testing.T
	c    *exprCtx
	toks []string
	i    int
	src  string
}

func (p *parser) peek() string {
	if p.i < len(p.toks) {
		return p.toks[p.i]
	}
	return ""
}

func (p *parser) next() string { tok := p.peek(); p.i++; return tok }

// or and and return an operand, not a boolean, as GitHub's do.
func (p *parser) or() any {
	v := p.and()
	for p.peek() == "||" {
		p.next()
		r := p.and()
		if !truthy(v) {
			v = r
		}
	}
	return v
}

func (p *parser) and() any {
	v := p.unary()
	for p.peek() == "&&" {
		p.next()
		r := p.unary()
		if truthy(v) {
			v = r
		}
	}
	return v
}

func (p *parser) unary() any {
	if p.peek() == "!" {
		p.next()
		return !truthy(p.unary())
	}
	l := p.primary()
	if op := p.peek(); op == "==" || op == "!=" {
		p.next()
		eq := strings.EqualFold(str(l), str(p.primary())) // GitHub compares strings ignoring case
		return eq == (op == "==")
	}
	return l
}

func (p *parser) primary() any {
	tok := p.next()
	switch {
	case tok == "(":
		v := p.or()
		if p.next() != ")" {
			p.t.Fatalf("expression %q: missing )", p.src)
		}
		return v
	case strings.HasPrefix(tok, "'"):
		return strings.Trim(tok, "'")
	case p.peek() == "(":
		p.next()
		if p.next() != ")" {
			p.t.Fatalf("expression %q: %s() takes no arguments here", p.src, tok)
		}
		switch tok {
		case "always":
			return true
		case "success":
			return !p.c.failed
		case "failure":
			return p.c.failed
		case "cancelled": //nolint:misspell // the function GitHub names
			return false
		}
		p.t.Fatalf("expression %q: unknown function %s", p.src, tok)
	case tok == "true" || tok == "false":
		return tok == "true"
	}
	return p.c.lookup(p.t, tok)
}

func truthy(v any) bool {
	switch v := v.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	}
	return true
}

func str(v any) string {
	switch v := v.(type) {
	case nil:
		return ""
	case bool:
		if v {
			return "true"
		}
		return "false"
	}
	return fmt.Sprint(v)
}

// TestExpressions checks the evaluator against GitHub's documented rules.
func TestExpressions(t *testing.T) {
	c := &exprCtx{inputs: map[string]string{"mode": "up", "upload": "true"}, steps: map[string]map[string]string{"check": {"exit-code": "1"}, "up": nil},
		github: map[string]string{"event_name": "pull_request"}}
	for src, want := range map[string]string{
		"inputs.mode != 'up'":                        "false",
		"always() && inputs.upload == 'TRUE'":        "true",
		"steps.check.outputs.exit-code == '1'":       "true",
		"steps.up.outputs.url || 'none'":             "none",
		"steps.check.outputs.exit-code || 'x'":       "1",
		"!(failure() || inputs.mode == 'stop')":      "true",
		"github.event_name == 'pull_request' && 'y'": "y",
	} {
		if got := str(c.eval(t, src)); got != want {
			t.Errorf("%s = %q, want %q", src, got, want)
		}
	}
}

// realTempDir is t.TempDir with symlinks resolved. On macOS the temp
// folder is under /var, which links to /private/var; the steps' $PWD is
// the resolved path. A runner's workspace has no links either.
func realTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
