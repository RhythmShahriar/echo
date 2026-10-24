// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package mcp

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/internal/flagtext"
	"github.com/RhythmShahriar/echo/runner"
	"github.com/RhythmShahriar/echo/sendmail"
	"github.com/RhythmShahriar/echo/store"
)

// Usage is the help for rhythm echo mcp.
const Usage = `rhythm echo mcp: MCP server for coding agents, on stdin and stdout

Usage:
  rhythm echo mcp [--url URL] [--token T] [--config FILE]

Runs Echo's MCP server on stdin and stdout, for coding agents:
  claude mcp add echo -- rhythm-echo mcp

Tools: list_messages, get_message, latest_code, run_action, snapshot,
compare, check_domain. Results are masked. Start Echo first (rhythm echo up);
the action tools use the nearest .echo/config.yaml (in the folder the agent
starts in or above it) and run in the folder that holds it.

Flags:
      --url <url>      where Echo runs (default from the config, or RHYTHM_ECHO_URL)
      --token <t>      the token Echo was started with (RHYTHM_ECHO_TOKEN)
      --config <file>  the config to read (default the nearest .echo/config.yaml)

Exit codes: 0 stdin closed, 2 usage error or a bad config, 3 couldn't read
or write.
` + flagtext.Footer

// Main runs the server until stdin closes, for rhythm echo mcp. It
// returns 0 then, 2 for a wrong command line or config and 3 when
// reading or writing fails. Nothing but protocol messages goes to stdout.
func Main(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	url := fs.String("url", "", "")
	token := fs.String("token", "", "")
	path := fs.String("config", "", "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stderr, Usage)
			return 0
		}
		fmt.Fprintf(stderr, "rhythm echo mcp: %s\n", flagtext.Error(err, fs))
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "rhythm echo mcp: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	p := *path
	if p != "" {
		if _, err := os.Stat(p); err != nil {
			fmt.Fprintf(stderr, "rhythm echo mcp: --config %s: %v\n", p, err)
			return 2
		}
	} else {
		var ue *sendmail.UntrustedError
		found, err := sendmail.FindConfig(".")
		switch {
		case err == nil:
			p = found
		case errors.As(err, &ue):
			fmt.Fprintf(stderr, "rhythm echo mcp: %v (someone else could have put it there); pass --config %s if you trust it\n", ue, ue.Path)
		case !errors.Is(err, sendmail.ErrNoConfig):
			fmt.Fprintln(stderr, "rhythm echo mcp:", err)
			return 2
		}
	}
	cfg, err := config.Load(cmp.Or(p, config.Path()))
	if err != nil {
		fmt.Fprintln(stderr, "rhythm echo mcp:", err)
		return 2
	}
	root, dir := projectFolders(p)
	s := &Server{
		Echo:    client.New(cmp.Or(*url, os.Getenv("RHYTHM_ECHO_URL"), "http://"+cfg.Listen), cmp.Or(*token, os.Getenv("RHYTHM_ECHO_TOKEN"))),
		Config:  cfg,
		Store:   store.Store{Root: root},
		Dir:     dir,
		Version: Version,
	}
	if Replay != nil {
		s.Replay = Replay(cfg, cmp.Or(dir, "."), p)
	}
	// No signal handling: an interrupt ends the process as usual. A
	// client that is done closes stdin, which ends Serve.
	if err := s.Serve(context.Background(), stdin, stdout); err != nil {
		fmt.Fprintln(stderr, "rhythm echo mcp:", err)
		return 3
	}
	return 0
}

// projectFolders returns where the server keeps snapshots and runs and
// where actions run for the config at path, as the CLI does: the
// config's own folder (proj/.echo) and the folder that holds .echo/
// (proj). With no config file both are the defaults: ./.echo and the
// working folder ("").
func projectFolders(path string) (root, dir string) {
	if path == "" {
		return config.Dir, ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return config.Dir, ""
	}
	root = filepath.Dir(abs)
	if filepath.Base(root) == config.Dir {
		return root, filepath.Dir(root)
	}
	return root, root
}

// Version is reported in serverInfo and stored runs. The CLI sets it to
// its own version before calling Main.
var Version = "dev"

// Replay, when set, makes the replayer for replay actions from the config.
// It lives in the CLI (it signs events with the site's secrets), which
// sets it before calling Main; without it replay actions report that
// replay isn't available.
var Replay func(cfg config.Config, dir, cfgPath string) runner.Replayer
