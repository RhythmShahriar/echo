// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Command peakmem runs a command and writes its own peak resident memory
// (in KiB, children left out) to a file: the consent benchmark's runner
// wraps rhythm-echo in it in Docker mode, so the figure is Echo's and
// not that of the Chrome it starts and reaps.
//
//	peakmem -o /bench/work/c01/good/.maxrss-kib rhythm-echo consent ...
//
// SIGINT and SIGTERM go on to the command; peakmem exits with the
// command's exit code. The file is written only when the mark could be
// read (Linux).
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/RhythmShahriar/echo/testbed/consent/bench/hwm"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("peakmem", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("o", "", "the file the peak goes to, in KiB")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *out == "" || fs.NArg() == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: peakmem -o FILE COMMAND [ARG...]")
		return 2
	}
	cmd := exec.Command(fs.Arg(0), fs.Args()[1:]...) //nolint:gosec,noctx // the command it was asked to run
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)
	if err := cmd.Start(); err != nil {
		_, _ = fmt.Fprintln(stderr, "peakmem:", err)
		return 127
	}
	w := hwm.Watch(cmd.Process.Pid)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-sigs:
				_ = cmd.Process.Signal(s)
			case <-done:
				return
			}
		}
	}()
	err := cmd.Wait()
	close(done)
	if peak := w.Stop(); peak > 0 {
		if werr := os.WriteFile(*out, []byte(strconv.FormatInt(peak>>10, 10)+"\n"), 0o600); werr != nil {
			_, _ = fmt.Fprintln(stderr, "peakmem:", werr)
		}
	}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exitErr):
		if code := exitErr.ExitCode(); code >= 0 {
			return code
		}
		return 128 + int(signalOf(exitErr))
	default:
		_, _ = fmt.Fprintln(stderr, "peakmem:", err)
		return 1
	}
}

// signalOf is the signal that ended the command, the shell's way of
// telling it apart from an exit code.
func signalOf(e *exec.ExitError) syscall.Signal {
	if ws, ok := e.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return ws.Signal()
	}
	return 0
}
