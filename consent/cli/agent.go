// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
)

// AgentCheck runs a consent check for a coding agent (the MCP tool
// check_consent). args are the check's command line, which the caller
// builds; the check writes nothing but its evidence folder and the
// JSON envelope (kind consent, with its issues and inconclusive runs),
// which AgentCheck returns. Hits are always held: a line asking for
// --send-hits is refused. When the check couldn't run, the error is
// the message the command would print, without "rhythm echo: ".
// Canceling ctx stops the runs and returns ctx's error.
func AgentCheck(ctx context.Context, args []string, env Env) ([]byte, error) {
	var out, errOut bytes.Buffer
	env.Stdout, env.Stderr, env.Open, env.Update = &out, &errOut, nil, nil
	if env.Stdin == nil {
		env.Stdin = strings.NewReader("")
	}
	if s := subOf(newFlagSet(&options{}), args); s != subCheck {
		return nil, fmt.Errorf("check_consent runs the check itself, not consent %s", s)
	}
	o, pos, err := parse(subCheck, args)
	if err != nil {
		return nil, err
	}
	if o.sendHits {
		return nil, errors.New("--send-hits isn't available to agents: hits are always held in the browser")
	}
	// JSON on stdout and nothing else: no notice, no tips, the view on
	// stderr (dropped).
	o.json, o.quiet = true, true
	c := &cmd{env: env, o: o, sub: subCheck}
	code := c.runCheck(ctx, pos)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if out.Len() == 0 {
		return nil, errors.New(agentMessage(errOut.String(), code))
	}
	return out.Bytes(), nil
}

// agentMessage is what the check printed on stderr, its lines without
// Echo's "rhythm echo: " prefix, or the exit code when it printed
// nothing.
func agentMessage(stderr string, code int) string {
	var lines []string
	for l := range strings.SplitSeq(strings.TrimSpace(stderr), "\n") {
		if l = strings.TrimSpace(strings.TrimPrefix(l, "rhythm echo: ")); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return fmt.Sprintf("the consent check exited %d without a result", code)
	}
	return strings.Join(lines, "\n")
}
