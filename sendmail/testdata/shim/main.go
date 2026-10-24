// Command shim is sendmail.Main as a standalone binary, for the Docker
// test (the real one is `rhythm-echo sendmail`).
package main

import (
	"os"

	"github.com/RhythmShahriar/echo/sendmail"
)

func main() { os.Exit(sendmail.Main(os.Args[1:], os.Stdin, os.Stderr)) }
