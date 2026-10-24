// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Command plugin-zip writes Echo Staging Seal as a zip, the form
// WordPress.org and Plugins > Add New > Upload take:
//
//	GOWORK=off go run ./connectors/wordpress/cmd/plugin-zip -o echo-staging-seal.zip
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/RhythmShahriar/echo/connectors/wordpress"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("plugin-zip", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("o", wordpress.PluginSlug+".zip", "the zip to write")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := write(*out); err != nil {
		fmt.Fprintln(stderr, "plugin-zip:", err)
		return 1
	}
	return 0
}

func write(path string) error {
	f, err := os.Create(path) //nolint:gosec // the caller names the file to write
	if err != nil {
		return err
	}
	if err := wordpress.PluginZip(f); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
