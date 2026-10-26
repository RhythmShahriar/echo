// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/testbed/consent/server"
)

// Bed is the testbed a benchmark runs against: where the scenario
// sites are served, how their state is switched, how the tracker sink is
// read and how rhythm-echo is started next to them.
type Bed interface {
	// SetBroken switches scenario id to its broken or good state.
	SetBroken(id string, broken bool) error
	// ResetSink clears the sink's counters for id.
	ResetSink(id string) error
	// Count is what reached the sink for id since its last reset.
	Count(id string) (server.Count, error)
	// Hits is the tracker hits recorded for id, oldest first.
	Hits(id string) ([]server.Hit, error)
	// Render serves a page the way the bed's sites do, in the state they
	// are in now, for the stand-in check.
	Render() http.Handler
	// Command is rhythm-echo with args, run in the work folder's
	// subfolder rel; the map flags are added in Go mode.
	Command(ctx context.Context, rel string, args []string) *exec.Cmd
	// EchoURL is what {echo} in a scenario's args becomes; "" when no
	// Echo runs for --echo.
	EchoURL() string
	// Close stops what the bed started.
	Close()
}

// goBed is Go mode: the testbed server in this process on one port,
// rhythm-echo on this machine with --map-host flags to reach it.
type goBed struct {
	srv   *server.Server
	addr  string // the sites listener, 127.0.0.1:23400
	bin   string
	work  string
	env   []string
	echo  string // {echo}
	sites net.Listener
}

// newGoBed serves the testbed at root on listen (sites and tags) in this
// process. echoProxy and echoCA, when set, let c18's server send its
// Conversions API event through Echo.
func newGoBed(root, listen, bin, work string, env []string, echoURL, echoProxy, echoCA string) (*goBed, error) {
	srv, err := server.New(server.Options{Root: root, Admin: "127.0.0.1:1", EchoProxy: echoProxy, EchoCA: echoCA})
	if err != nil {
		return nil, err
	}
	l, err := net.Listen("tcp", listen)
	if err != nil {
		return nil, fmt.Errorf("the testbed's sites on %s: %w (another testbed running? make -C testbed/consent serve uses the same port)", listen, err)
	}
	b := &goBed{srv: srv, addr: l.Addr().String(), bin: bin, work: work, env: env, echo: echoURL, sites: l}
	go func() { _ = srv.Serve(l) }()
	return b, nil
}

func (b *goBed) SetBroken(id string, broken bool) error { return b.srv.SetBroken(id, broken) }
func (b *goBed) ResetSink(id string) error              { b.srv.ResetScenario(id); return nil }
func (b *goBed) Count(id string) (server.Count, error)  { return b.srv.Count(id), nil }
func (b *goBed) Hits(id string) ([]server.Hit, error)   { return b.srv.Hits(id), nil }
func (b *goBed) Render() http.Handler                   { return b.srv.Handler() }
func (b *goBed) EchoURL() string                        { return b.echo }
func (b *goBed) Close()                                 { _ = b.sites.Close() }

// mapArgs are the flags that send every testbed name to this bed and
// nothing anywhere else.
func (b *goBed) mapArgs() []string { return server.MapArgs(b.addr) }

func (b *goBed) Command(ctx context.Context, rel string, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, b.bin, append(args, b.mapArgs()...)...) //nolint:gosec // the binary under test
	cmd.Dir = filepath.Join(b.work, filepath.FromSlash(rel))
	cmd.Env = b.env
	return cmd
}

// Docker mode's fixed names: the Compose project and file, the runner's
// work folder and the admin port the gate publishes.
const (
	composeProject = "echo-consent-testbed"
	runnerBench    = "/bench" // testbed/consent/bench, bind-mounted in the runner
	dockerAdmin    = "127.0.0.1:18951"
	dockerEcho     = "http://echo:8025"
)

// dockerBed is sealed Docker mode: the sites run in containers, the
// state files are shared with them through a bind mount and rhythm-echo
// runs inside the runner, which has no route out.
type dockerBed struct {
	local   *server.Server // over the same state folder, for Render and SetBroken
	compose string         // the docker-compose.yml
	work    string         // the work folder as the runner sees it, under /bench
	admin   string         // the sink and state endpoints
	client  *http.Client
}

// newDockerBed connects to the Docker testbed of root. work must be
// under root/bench, the folder the runner mounts.
func newDockerBed(root, work string) (*dockerBed, error) {
	rel, err := filepath.Rel(filepath.Join(root, "bench"), work)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("-work %s: Docker mode needs a folder under %s, which the runner mounts", work, filepath.Join(root, "bench"))
	}
	srv, err := server.New(server.Options{Root: root})
	if err != nil {
		return nil, err
	}
	b := &dockerBed{local: srv, compose: filepath.Join(root, "docker-compose.yml"), work: path.Join(runnerBench, filepath.ToSlash(rel)), admin: "http://" + dockerAdmin,
		client: &http.Client{Timeout: 10 * time.Second}}
	if _, err := b.get("/_admin/health"); err != nil {
		return nil, fmt.Errorf("the Docker testbed doesn't answer on %s (make -C testbed/consent up): %w", dockerAdmin, err)
	}
	return b, nil
}

func (b *dockerBed) SetBroken(id string, broken bool) error { return b.local.SetBroken(id, broken) }
func (b *dockerBed) Render() http.Handler                   { return b.local.Handler() }
func (b *dockerBed) EchoURL() string                        { return dockerEcho }
func (b *dockerBed) Close()                                 {}

func (b *dockerBed) get(p string) ([]byte, error) {
	resp, err := b.client.Get(b.admin + p) //nolint:noctx // a loopback admin port with a client timeout
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %s", p, resp.Status)
	}
	return body, nil
}

func (b *dockerBed) ResetSink(id string) error {
	req, err := http.NewRequest(http.MethodPost, b.admin+"/_sink/reset?scenario="+url.QueryEscape(id), nil) //nolint:noctx // as get
	if err != nil {
		return err
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return errors.New("sink reset answered " + resp.Status)
	}
	return nil
}

func (b *dockerBed) Count(id string) (server.Count, error) {
	var c server.Count
	body, err := b.get("/_sink/count?scenario=" + url.QueryEscape(id))
	if err == nil {
		err = json.Unmarshal(body, &c)
	}
	return c, err
}

func (b *dockerBed) Hits(id string) ([]server.Hit, error) {
	var h []server.Hit
	body, err := b.get("/_sink/hits?scenario=" + url.QueryEscape(id))
	if err == nil {
		err = json.Unmarshal(body, &h)
	}
	return h, err
}

// Command runs rhythm-echo in the runner, through GNU time so the
// child's peak memory is measured inside the container (see rssFile).
func (b *dockerBed) Command(ctx context.Context, rel string, args []string) *exec.Cmd {
	dir := path.Join(b.work, rel)
	full := append([]string{"compose", "-p", composeProject, "-f", b.compose, "exec", "-T", "-w", dir, "runner",
		"/usr/bin/time", "-f", "%M", "-o", path.Join(dir, rssFile), "rhythm-echo"}, args...)
	return exec.CommandContext(ctx, "docker", full...) //nolint:gosec // docker with the benchmark's own arguments
}

// rssFile is where Docker mode's runs leave the child's peak resident
// memory in KiB, in the run's folder.
const rssFile = ".maxrss-kib"
