// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// The probes that need the Docker testbed. They use the benchmark's Echo
// (p.Site.Echo) and the testbed's config, as the site scenarios do.
func init() {
	probes["large-laravel"] = largeLaravel
	probes["unreachable"] = unreachable
	probes["noise"] = noise
	probes["version-skew"] = versionSkew
}

// siteStep runs an Echo command against the testbed as the named step,
// like ProbeEnv.step with the benchmark Echo's environment and binary.
func (p *ProbeEnv) siteStep(ctx context.Context, name, dir string, args ...string) Observation {
	q := *p
	q.Bin = p.Site.Echo.Bin
	return q.stepEnv(ctx, name, dir, p.Site.Echo.env(), append(args, "--config", p.Site.Echo.Config())...)
}

// setState switches the testbed state file for an environment scenario,
// which the fixtures read like a site scenario's (state/<id>).
func (p *ProbeEnv) setState(id string, broken bool) error {
	f := filepath.Join(p.Testbed, "state", id)
	if !broken {
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return os.WriteFile(f, nil, 0o644) //nolint:gosec // the containers read it as another user
}

// largeLaravel: the Laravel app sends a 2 MB HTML newsletter with a 5 MB
// PDF through its connector, which waits 2 s for Echo. It must be
// captured and faked, not blocked or failed by the timeout.
func largeLaravel(ctx context.Context, p *ProbeEnv, broken bool) ([]Observation, error) {
	if err := p.Site.Echo.Start(ctx, "seal"); err != nil {
		return nil, err
	}
	if err := p.setState("e06", broken); err != nil {
		return nil, err
	}
	defer func() { _ = p.setState("e06", false) }()
	return []Observation{p.siteStep(ctx, "snapshot", p.Work, "snapshot", "--action", "Laravel newsletter", "--name", "newsletter", "--fail-on", "none")}, nil
}

// unreachableActions are the actions whose sites hold a connector, one per
// stack, plus the plain-PHP page whose mail() goes through sendmail.
var unreachableActions = []struct{ step, action string }{
	{"wordpress", "Contact form"},
	{"laravel", "Laravel subscription order"},
	{"next", "Next.js signup"},
	{"php", "Wholesale order (plain PHP)"},
}

// unreachable: Echo runs while the sites can't reach it (it moved off the
// ports the gate forwards). Sealed, each connector must block every send
// and the comparison must say why nothing arrived, not only that it
// didn't.
func unreachable(ctx context.Context, p *ProbeEnv, broken bool) ([]Observation, error) {
	e := p.Site.Echo
	defer func() { e.Stop(); e.Ports = nil }()
	var obs []Observation
	for _, a := range unreachableActions {
		dir := filepath.Join(p.Work, a.step)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, err
		}
		e.Ports = nil
		if err := e.Start(ctx, "seal"); err != nil {
			return nil, err
		}
		if o := p.siteStep(ctx, "baseline", dir, "snapshot", "--action", a.action, "--name", "good", "--fail-on", "none"); o.Exit != 0 {
			return nil, fmt.Errorf("the %s baseline exited %d: %s", a.step, o.Exit, o.Output)
		}
		if broken {
			e.Ports = []string{"18863", "18864", "18865"}
			if err := e.Start(ctx, "seal"); err != nil {
				return nil, err
			}
		}
		obs = append(obs, p.siteStep(ctx, a.step, dir, "compare", "--action", a.action, "--snapshot", "good", "--fail-on", "none"))
	}
	return obs, nil
}

// noiseScenarios are the site scenarios whose emails carry an order
// number, a date or a link that changes on every run.
var noiseScenarios = []string{"s10", "s11", "s12"}

// noise: the order number, the date and the order link differ on every
// run. Comparing two runs of the good state must report nothing; the
// broken states of s10 to s12 only what broke, never CHANGED text.
func noise(ctx context.Context, p *ProbeEnv, broken bool) ([]Observation, error) {
	var obs []Observation
	for _, id := range noiseScenarios {
		i := slices.IndexFunc(p.Site.Manifest.Scenarios, func(s Scenario) bool { return s.ID == id })
		if i < 0 {
			return nil, fmt.Errorf("no scenario %s in the manifest", id)
		}
		s := p.Site.Manifest.Scenarios[i]
		dir := filepath.Join(p.Work, id)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, err
		}
		o, err := p.siteCompare(ctx, s, dir, broken)
		if err != nil {
			return nil, err
		}
		obs = append(obs, o)
	}
	return obs, nil
}

// siteCompare snapshots a site scenario's action in the good state, then
// compares it in the good or the broken state, as the step named after
// the scenario.
func (p *ProbeEnv) siteCompare(ctx context.Context, s Scenario, dir string, broken bool) (Observation, error) {
	if err := Toggle(p.Testbed, s.ID, "good"); err != nil {
		return Observation{}, err
	}
	defer func() { _ = Toggle(p.Testbed, s.ID, "good") }()
	if err := p.Site.Echo.Start(ctx, s.Mode); err != nil {
		return Observation{}, err
	}
	for _, line := range p.Site.Manifest.Reset {
		_ = p.Site.Echo.Shell(ctx, p.Timeout, line)
	}
	if o := p.siteStep(ctx, "baseline", dir, "snapshot", "--action", s.Action, "--name", "good", "--fail-on", "none"); o.Exit != 0 {
		return Observation{}, fmt.Errorf("the %s baseline exited %d: %s", s.ID, o.Exit, o.Output)
	}
	if broken {
		if err := Toggle(p.Testbed, s.ID, "broken"); err != nil {
			return Observation{}, err
		}
	}
	o := p.siteStep(ctx, s.ID, dir, "compare", "--action", s.Action, "--snapshot", "good", "--fail-on", "none")
	for _, c := range o.Changes {
		o.Output += "\n" + describe(c)
	}
	return o, nil
}

// versionSkew: the WordPress connector of Echo 1.0.0 (wire 1) against
// this Echo and this connector against Echo 1.0.0. Mail must be captured
// either way. Echo 1.0.0 is built from its git tag; the good state is
// this connector against this Echo.
func versionSkew(ctx context.Context, p *ProbeEnv, broken bool) ([]Observation, error) {
	e := p.Site.Echo
	current := e.Bin
	old := current
	if broken {
		var err error
		if old, err = p.Site.oldEcho(ctx, p.Root); err != nil {
			return nil, err
		}
	}
	defer func() {
		e.Stop()
		e.Bin = current
		_ = installConnector(ctx, p, current)
	}()
	var obs []Observation
	// Its connector against this Echo.
	if err := installConnector(ctx, p, old); err != nil {
		return nil, err
	}
	e.Bin = current
	if err := e.Start(ctx, "seal"); err != nil {
		return nil, err
	}
	o := p.siteStep(ctx, "old connector", filepath.Join(p.Work, "connector"), "snapshot", "--action", "Contact form", "--name", "skew", "--fail-on", "none")
	obs = append(obs, o)
	// This connector against its Echo, run with its own CLI.
	if err := installConnector(ctx, p, current); err != nil {
		return nil, err
	}
	e.Stop()
	e.Bin = old
	if err := e.Start(ctx, "seal"); err != nil {
		return nil, err
	}
	q := *p
	q.Site = &SiteBench{Echo: e, Manifest: p.Site.Manifest, Options: p.Site.Options}
	obs = append(obs, q.siteStep(ctx, "old Echo", filepath.Join(p.Work, "echo"), "snapshot", "--action", "Contact form", "--name", "skew", "--fail-on", "none"))
	return obs, nil
}

// installConnector writes bin's WordPress connector into the testbed's
// mu-plugins, as make connector does.
func installConnector(ctx context.Context, p *ProbeEnv, bin string) error {
	exit, out := p.run(ctx, p.Testbed, os.Environ(), bin, "install", "wordpress", "wp", "--url", "http://gate:18860", "--token", echoToken,
		"--proxy", "http://echo:"+echoToken+"@gate:18862", "--ca", "/echo-ca/ca.pem")
	if exit != 0 {
		return fmt.Errorf("%s install wordpress exited %d: %s", filepath.Base(bin), exit, strings.TrimSpace(out))
	}
	return nil
}

// oldEchoTag is the release the version skew probe pits this one against.
const oldEchoTag = "v1.0.0"

// oldEcho builds Echo 1.0.0 from its git tag, once, with the modules
// already in the Go cache (no download).
func (s *SiteBench) oldEcho(ctx context.Context, root string) (string, error) {
	s.old.once.Do(func() {
		dir, err := os.MkdirTemp("", "echo-"+oldEchoTag+"-*")
		if err != nil {
			s.old.err = err
			return
		}
		archive := exec.CommandContext(ctx, "sh", "-c", `git -C "$1" archive "$2" | tar -x -C "$3"`, "sh", root, oldEchoTag, dir) //nolint:gosec // our own tag
		if out, err := archive.CombinedOutput(); err != nil {
			s.old.err = fmt.Errorf("git archive %s: %w: %s", oldEchoTag, err, out)
			return
		}
		goBin, err := exec.LookPath("go")
		if err != nil {
			goBin = "/usr/local/go/bin/go"
		}
		bin := filepath.Join(dir, "rhythm-echo-"+strings.TrimPrefix(oldEchoTag, "v"))
		build := exec.CommandContext(ctx, goBin, "build", "-o", bin, "./cmd/rhythm-echo") //nolint:gosec // building a release of the tool under test
		build.Dir, build.Env = dir, append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GOPROXY=off")
		if out, err := build.CombinedOutput(); err != nil {
			s.old.err = fmt.Errorf("building Echo %s (its modules must be in the Go cache): %w: %s", oldEchoTag, err, out)
			return
		}
		s.old.bin = bin
	})
	return s.old.bin, s.old.err
}
