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

	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/runner"
	"github.com/RhythmShahriar/echo/store"
)

// Usage is the help for rhythm echo mcp.
const Usage = `usage: rhythm echo mcp [--url URL] [--token T] [--config FILE]

Runs Echo's MCP server on stdin and stdout, for coding agents:
  claude mcp add echo -- rhythm-echo mcp

Tools: list_messages, get_message, latest_code, run_action, snapshot,
compare, check_domain. Results are masked. Start Echo first (rhythm echo up);
the action tools use .echo/config.yaml in the folder the agent starts in.

  --url <url>      where Echo runs (default from the config, or RHYTHM_ECHO_URL)
  --token <t>      the token Echo was started with (RHYTHM_ECHO_TOKEN)
  --config <file>  the config to read (default .echo/config.yaml)
`

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
		fmt.Fprintf(stderr, "rhythm echo mcp: %v\n\n%s", err, Usage)
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "rhythm echo mcp: unexpected argument %q\n\n%s", fs.Arg(0), Usage)
		return 2
	}
	p := cmp.Or(*path, config.Path())
	if *path != "" {
		if _, err := os.Stat(p); err != nil {
			fmt.Fprintf(stderr, "rhythm echo mcp: --config %s: %v\n", p, err)
			return 2
		}
	}
	cfg, err := config.Load(p)
	if err != nil {
		fmt.Fprintln(stderr, "rhythm echo mcp:", err)
		return 2
	}
	s := &Server{
		Echo:    client.New(cmp.Or(*url, os.Getenv("RHYTHM_ECHO_URL"), "http://"+cfg.Listen), cmp.Or(*token, os.Getenv("RHYTHM_ECHO_TOKEN"))),
		Config:  cfg,
		Store:   store.Store{Root: config.Dir},
		Version: Version,
	}
	if Replay != nil {
		s.Replay = Replay(cfg)
	}
	// No signal handling: an interrupt ends the process as usual. A
	// client that is done closes stdin, which ends Serve.
	if err := s.Serve(context.Background(), stdin, stdout); err != nil {
		fmt.Fprintln(stderr, "rhythm echo mcp:", err)
		return 3
	}
	return 0
}

// Version is reported in serverInfo and stored runs. The CLI sets it to
// its own version before calling Main.
var Version = "dev"

// Replay, when set, makes the replayer for replay actions from the config.
// It lives in the CLI (it signs events with the site's secrets), which
// sets it before calling Main; without it replay actions report that
// replay isn't available.
var Replay func(config.Config) runner.Replayer
