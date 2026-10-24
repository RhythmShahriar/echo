package main

import (
	"fmt"
	"io"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/runner"
)

// These commands are wired to their packages as they land.

func notYet(stderr io.Writer, what string) int {
	fmt.Fprintf(stderr, "rhythm echo: %s isn't available in this build yet\n", what)
	return exitError
}

func cmdInstall(_ []string, _, stderr io.Writer) int { return notYet(stderr, "install") }
func cmdDoctor(s *session) int                       { return notYet(s.stderr, "doctor") }
func cmdCheck(s *session) int                        { return notYet(s.stderr, "check") }
func cmdHeaders(s *session) int                      { return notYet(s.stderr, "headers") }
func cmdReplay(s *session) int                       { return notYet(s.stderr, "replay") }
func cmdCA(_ []string, _, stderr io.Writer) int      { return notYet(stderr, "ca") }
func cmdSendmail(_ []string, _ io.Reader, stderr io.Writer) int {
	return notYet(stderr, "sendmail")
}

func replayer(config.Config) runner.Replayer { return nil }

func startProxy(s *session, _ *catcher.Server) (func(), error) {
	if s.cfg.Proxy != "" {
		return nil, fmt.Errorf("the proxy isn't available in this build yet")
	}
	return func() {}, nil
}
