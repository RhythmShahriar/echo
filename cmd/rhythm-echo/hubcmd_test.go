// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestMCPCommand(t *testing.T) {
	t.Chdir(t.TempDir())
	in := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}` + "\n"
	var out, errOut bytes.Buffer
	code := run([]string{"mcp", "--url", "http://127.0.0.1:21349"}, strings.NewReader(in), &out, &errOut)
	var resp struct {
		Result struct {
			ServerInfo struct{ Name, Version string } `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); code != exitOK || err != nil || resp.Result.ServerInfo.Version != version {
		t.Errorf("mcp: %d %v\n%s\n%s", code, err, out.String(), errOut.String())
	}
}

func TestMCPHubAndScheduleAreCommands(t *testing.T) {
	for _, c := range []struct {
		name, want string
	}{
		{"mcp", "Usage:\n  rhythm echo mcp"},
		{"hub", "Usage:\n  rhythm echo hub --store DIR --sites FILE"},
		{"schedule", "Usage:\n  rhythm echo schedule \"<when>\""},
		{"conformance", "Usage:\n  rhythm echo conformance [flags]"},
		{"init", "Usage:\n  rhythm echo init [--stack s] [--force]"},
		{"ca", "Usage:\n  rhythm echo ca [--pem | --untrust]"},
		{"sendmail", "Usage:\n  rhythm echo sendmail [-t]"},
	} {
		// Every command's help reads alike: what it does, Usage, its
		// flags, the exit codes and the pointer to the rest.
		code, out, _ := runCLI(t, "help", c.name)
		if code != exitOK || !strings.HasPrefix(out, "rhythm echo "+c.name+": ") || !strings.Contains(out, c.want) ||
			!strings.Contains(out, "\nFlags:\n") || !strings.Contains(out, "\nExit codes") || !strings.HasSuffix(out, "\nrhythm echo help lists every command.\n") {
			t.Errorf("help %s: %d\n%s", c.name, code, out)
		}
	}
	// sendmail's help says in order where the message goes and lists the
	// variables it reads; --help prints it too.
	code, out, _ := runCLI(t, "sendmail", "--help")
	if code != exitOK {
		t.Errorf("sendmail --help: %d\n%s", code, out)
	}
	for _, want := range []string{"RHYTHM_ECHO_SMTP", "RHYTHM_ECHO_URL", "RHYTHM_ECHO_TOKEN", "RHYTHM_ECHO_RUN", "RHYTHM_ECHO_FORCE", "127.0.0.1:1025", "the first that is set"} {
		if !strings.Contains(out, want) {
			t.Errorf("sendmail --help lacks %q:\n%s", want, out)
		}
	}
	if code, _, errOut := runCLI(t, "sendmail", "--bogus"); code != 64 || !strings.Contains(errOut, "unknown option --bogus") {
		t.Errorf("sendmail --bogus: %d %s", code, errOut)
	}
	// Their flag errors read like the other commands', without the help.
	for _, c := range []struct{ args, want string }{
		{"hub --bogus", "rhythm echo hub: unknown flag --bogus\n"},
		{"hub --stor x", "rhythm echo hub: unknown flag --stor; did you mean --store?\n"},
		{"schedule --bogus", "rhythm echo schedule: unknown flag --bogus\n"},
		{"conformance --bogus", "rhythm echo conformance: unknown flag --bogus\n"},
		{"conformance --listen", "rhythm echo conformance: --listen needs a value\n"},
		{"mcp --bogus", "rhythm echo mcp: unknown flag --bogus\n"},
	} {
		if code, _, errOut := runCLI(t, strings.Fields(c.args)...); code != exitUsage || errOut != c.want {
			t.Errorf("%s: %d %q, want %q", c.args, code, errOut, c.want)
		}
	}
	if code, _, errOut := runCLI(t, "hub"); code != exitUsage || !strings.Contains(errOut, "--store and --sites are required") {
		t.Errorf("hub without flags: %d %s", code, errOut)
	}
	if code, _, errOut := runCLI(t, "schedule", "--now"); code != exitUsage || !strings.Contains(errOut, "--store is required") {
		t.Errorf("schedule without a store: %d %s", code, errOut)
	}
	var b bytes.Buffer
	usage(&b)
	for _, line := range []string{"rhythm echo mcp ", "rhythm echo hub ", "rhythm echo schedule "} {
		if !strings.Contains(b.String(), line) {
			t.Errorf("the main help lacks %q", line)
		}
	}
}
