package main

import (
	"fmt"
	"io"
)

// These commands are wired to their packages as they land.

func notYet(stderr io.Writer, what string) int {
	fmt.Fprintf(stderr, "rhythm echo: %s isn't available in this build yet\n", what)
	return exitError
}

func cmdInstall(_ []string, _, stderr io.Writer) int { return notYet(stderr, "install") }
func cmdDoctor(s *session) int                       { return notYet(s.stderr, "doctor") }
