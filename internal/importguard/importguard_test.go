// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package importguard

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

const module = "github.com/RhythmShahriar/echo"

// listed is one package as go list -json describes it.
type listed struct {
	ImportPath   string
	Imports      []string
	TestImports  []string
	XTestImports []string
	Deps         []string
	Module       *struct{ Path string }
}

var (
	listOnce sync.Once
	pkgs     []listed
	deps     []listed
	listErr  error
)

// golist runs go list -json with args from the module root, without the
// workspace, as a release build sees it.
func golist(args ...string) ([]listed, error) {
	gobin, err := exec.LookPath("go")
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(gobin, append([]string{"list", "-json"}, args...)...)
	cmd.Dir = filepath.Join("..", "..")
	cmd.Env = append(os.Environ(), "GOWORK=off")
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return nil, &listError{err, errOut.String()}
	}
	var all []listed
	dec := json.NewDecoder(&out)
	for {
		var p listed
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, err
		}
		all = append(all, p)
	}
	return all, nil
}

type listError struct {
	err    error
	stderr string
}

func (e *listError) Error() string { return e.err.Error() + ": " + e.stderr }

func load(t *testing.T) ([]listed, []listed) {
	t.Helper()
	listOnce.Do(func() {
		pkgs, listErr = golist("./...")
		if listErr == nil {
			deps, listErr = golist("-deps", "./...")
		}
	})
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go on PATH")
	}
	if listErr != nil {
		t.Fatal(listErr)
	}
	return pkgs, deps
}

// TestNoSoundcheckModule: Echo copies the browser package; it never
// depends on Soundcheck's module.
func TestNoSoundcheckModule(t *testing.T) {
	_, deps := load(t)
	if len(deps) == 0 {
		t.Fatal("go list -deps listed nothing")
	}
	for _, p := range deps {
		if p.Module != nil && strings.HasPrefix(p.Module.Path, "github.com/RhythmShahriar/soundcheck") {
			t.Errorf("%s comes from %s", p.ImportPath, p.Module.Path)
		}
	}
}

// browserFree are the packages that must never pull in rod or the
// browser package, directly or through another package.
var browserFree = []string{"model", "catcher", "client", "wire", "compare", "check", "report", "config", "consent",
	"consent/tracker", "consent/signal", "consent/cmp", "consent/verdict", "consent/evidence"}

func browserPath(p string) bool {
	return strings.HasPrefix(p, "github.com/go-rod/") || p == module+"/internal/browser" || strings.HasPrefix(p, module+"/internal/browser/")
}

// TestImportGuard: only consent/browse and cmd/rhythm-echo may reach the
// browser; consent/cli may link it through browse but never import rod
// itself.
func TestImportGuard(t *testing.T) {
	pkgs, _ := load(t)
	byPath := map[string]listed{}
	for _, p := range pkgs {
		byPath[p.ImportPath] = p
	}
	for _, name := range browserFree {
		p, ok := byPath[module+"/"+name]
		if !ok {
			t.Errorf("%s isn't in the build", name)
			continue
		}
		for _, d := range p.Deps {
			if browserPath(d) {
				t.Errorf("%s depends on %s", name, d)
			}
		}
	}
	if p, ok := byPath[module+"/consent/cli"]; ok {
		for _, imp := range p.Imports {
			if strings.HasPrefix(imp, "github.com/go-rod/") {
				t.Errorf("consent/cli imports %s; it reaches the browser only through consent/browse", imp)
			}
		}
	}
	// Nothing else under consent/ may import the browser either.
	for path, p := range byPath {
		rel, ok := strings.CutPrefix(path, module+"/consent/")
		if !ok || rel == "browse" || strings.HasPrefix(rel, "browse/") || rel == "cli" {
			continue
		}
		if i := slices.IndexFunc(p.Imports, browserPath); i >= 0 {
			t.Errorf("consent/%s imports %s", rel, p.Imports[i])
		}
	}
}

// TestNoExecInConsent: no consent package starts programs; the browser
// is started by internal/browser alone.
func TestNoExecInConsent(t *testing.T) {
	pkgs, _ := load(t)
	n := 0
	for _, p := range pkgs {
		if p.ImportPath != module+"/consent" && !strings.HasPrefix(p.ImportPath, module+"/consent/") {
			continue
		}
		n++
		for _, imp := range slices.Concat(p.Imports, p.TestImports, p.XTestImports) {
			if imp == "os/exec" {
				t.Errorf("%s imports os/exec", p.ImportPath)
			}
		}
	}
	if n < 8 {
		t.Errorf("found %d consent packages; the guard isn't looking where it should", n)
	}
}

// browserPath catches rod and the browser package with its children,
// and nothing that only shares a prefix.
func TestBrowserPath(t *testing.T) {
	for p, want := range map[string]bool{
		"github.com/go-rod/rod":                  true,
		"github.com/go-rod/rod/lib/proto":        true,
		module + "/internal/browser":             true,
		module + "/internal/browser/proc":        true,
		module + "/internal/browserish":          false,
		module + "/internal/testenv":             false,
		"github.com/ysmood/gson":                 false,
		"github.com/RhythmShahriar/soundcheck/x": false,
	} {
		if got := browserPath(p); got != want {
			t.Errorf("browserPath(%s) = %v; want %v", p, got, want)
		}
	}
}
