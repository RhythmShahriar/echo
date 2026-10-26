// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// exitEnv makes the test binary a command that exits with this code.
const exitEnv = "PEAKMEM_TEST_EXIT"

func TestMain(m *testing.M) {
	if code := os.Getenv(exitEnv); code != "" {
		n, _ := strconv.Atoi(code)
		os.Exit(n)
	}
	os.Exit(m.Run())
}

func TestRunWritesThePeakAndKeepsTheExitCode(t *testing.T) {
	t.Setenv(exitEnv, "3")
	out := filepath.Join(t.TempDir(), ".maxrss-kib")
	var stderr bytes.Buffer
	if code := run([]string{"-o", out, os.Args[0]}, &stderr); code != 3 {
		t.Fatalf("exit = %d (%s)", code, stderr.String())
	}
	data, err := os.ReadFile(out) //nolint:gosec // the test's own file
	if runtime.GOOS != "linux" {
		if err == nil {
			t.Errorf("a peak was written where none can be read: %q", data)
		}
		return
	}
	if err != nil {
		t.Skipf("the command ended before its mark could be read: %v", err)
	}
	kib, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil || kib <= 0 {
		t.Errorf("peak file = %q", data)
	}
}

func TestRunRefuses(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		args []string
		code int
		says string
	}{
		{nil, 2, "usage: peakmem -o FILE COMMAND"},
		{[]string{"-o", filepath.Join(dir, "x")}, 2, "usage: peakmem -o FILE COMMAND"},
		{[]string{os.Args[0]}, 2, "usage: peakmem -o FILE COMMAND"},
		{[]string{"-bogus"}, 2, "flag provided but not defined"},
		{[]string{"-o", filepath.Join(dir, "x"), filepath.Join(dir, "none")}, 127, "peakmem: "},
	} {
		var stderr bytes.Buffer
		if code := run(tc.args, &stderr); code != tc.code || !strings.Contains(stderr.String(), tc.says) {
			t.Errorf("run(%q) = %d, %q", tc.args, code, stderr.String())
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "x")); err == nil {
		t.Error("a refused run wrote a peak")
	}
}
