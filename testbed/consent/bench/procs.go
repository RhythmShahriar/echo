// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// commandLines lists the command line of every process on the machine
// this user may see: /proc on Linux, ps on macOS, CIM on Windows.
func commandLines(ctx context.Context) ([]string, error) {
	switch runtime.GOOS {
	case "linux":
		dirs, err := filepath.Glob("/proc/[0-9]*/cmdline")
		if err != nil {
			return nil, err
		}
		var out []string
		for _, d := range dirs {
			b, err := os.ReadFile(d) //nolint:gosec // /proc
			if err != nil || len(b) == 0 {
				continue
			}
			out = append(out, strings.ReplaceAll(string(b), "\x00", " "))
		}
		return out, nil
	case "windows":
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		b, err := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command",
			"Get-CimInstance Win32_Process | ForEach-Object { $_.CommandLine }").Output()
		return strings.Split(string(b), "\n"), err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, "ps", "-axww", "-o", "command=").Output()
	return strings.Split(string(b), "\n"), err
}

// processesWith counts the processes whose command line holds sub,
// case-insensitively on Windows, where paths are.
func processesWith(ctx context.Context, sub string) (int, error) {
	lines, err := commandLines(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, l := range lines {
		if runtime.GOOS == "windows" {
			l, sub = strings.ToLower(l), strings.ToLower(sub)
		}
		if strings.Contains(l, sub) {
			n++
		}
	}
	return n, nil
}

// entriesUnder counts the files and folders under dir (dir itself not
// counted); 0 when it doesn't exist. rod/ and rod/user-data/ are left
// out: rod makes them as the shared parents of every profile and never
// removes them (another launch may be using them), so only a profile
// or file inside them is a leftover.
func entriesUnder(dir string) int {
	scaffold := map[string]bool{filepath.Join(dir, "rod"): true, filepath.Join(dir, "rod", "user-data"): true}
	n := 0
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && p != dir && !(d.IsDir() && scaffold[p]) {
			n++
		}
		return nil
	})
	return n
}
