// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package hwm

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// helperEnv makes the test binary a helper process: "self" holds
// helperMiB itself, "parent" starts a "self" child, waits for it and
// holds nothing.
const helperEnv = "HWM_TEST_HELPER"

const helperMiB = 160

func TestMain(m *testing.M) {
	switch os.Getenv(helperEnv) {
	case "self":
		buf := make([]byte, helperMiB<<20)
		for i := 0; i < len(buf); i += 4096 {
			buf[i] = 1
		}
		time.Sleep(300 * time.Millisecond)
		runtime.KeepAlive(buf)
		os.Exit(0)
	case "parent":
		cmd := exec.Command(os.Args[0]) //nolint:gosec,noctx // the test binary itself
		cmd.Env = append(os.Environ(), helperEnv+"=self")
		if err := cmd.Run(); err != nil {
			os.Exit(2)
		}
		time.Sleep(100 * time.Millisecond)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int64
		ok   bool
	}{
		{"Name:\trhythm-echo\nVmPeak:\t  900000 kB\nVmHWM:\t   61440 kB\nVmRSS:\t   51200 kB\n", 61440 << 10, true},
		{"VmHWM: 0 kB\n", 0, true},
		// A zombie has released its memory and lists no Vm lines.
		{"Name:\trhythm-echo\nState:\tZ (zombie)\n", 0, false},
		{"", 0, false},
		{"VmHWM:\t61440 MB\n", 0, false},
		{"VmHWM:\t-5 kB\n", 0, false},
		{"VmHWM:\tlots kB\n", 0, false},
		{"VmHWM:\t1 2 kB\n", 0, false},
		{"VmHWM:\t99999999999999999999 kB\n", 0, false},
		{"XVmHWM:\t5 kB\n", 0, false},
	} {
		got, ok := Parse([]byte(tc.in))
		if got != tc.want || ok != tc.ok {
			t.Errorf("Parse(%q) = %d, %v; want %d, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte("VmHWM:\t61440 kB\n"))
	f.Add([]byte("State:\tZ (zombie)\n"))
	f.Add([]byte("VmHWM: 1 kB 2\nVmHWM: 3 kB\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		n, ok := Parse(data)
		if n < 0 || (!ok && n != 0) {
			t.Errorf("Parse = %d, %v", n, ok)
		}
	})
}

// watchHelper runs the test binary as the given helper and returns the
// peak Watch saw for it.
func watchHelper(t *testing.T, role string) int64 {
	t.Helper()
	cmd := exec.Command(os.Args[0]) //nolint:gosec,noctx // the test binary itself
	cmd.Env = append(os.Environ(), helperEnv+"="+role)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	w := Watch(cmd.Process.Pid)
	if err := cmd.Wait(); err != nil {
		t.Fatalf("%s helper: %v", role, err)
	}
	return w.Stop()
}

func TestWatchCountsTheProcessItself(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the per-process mark is read on Linux only")
	}
	if got := watchHelper(t, "self"); got < helperMiB<<20 {
		t.Errorf("peak of a process holding %d MiB = %s", helperMiB, mib(got))
	}
}

// The reason for this package: a parent that reaps a big child must not
// be charged for it, as ru_maxrss charges it.
func TestWatchLeavesOutChildren(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the per-process mark is read on Linux only")
	}
	got := watchHelper(t, "parent")
	if got == 0 || got >= 64<<20 {
		t.Errorf("peak of a parent whose child held %d MiB = %s, want its own few MiB", helperMiB, mib(got))
	}
}

func TestWatchUnknown(t *testing.T) {
	if runtime.GOOS == "linux" {
		if _, ok := Read(-1); ok {
			t.Error("a pid that can't exist was read")
		}
	}
	w := Watch(-1)
	if got := w.Stop(); got != 0 {
		t.Errorf("peak of no process = %d", got)
	}
	if got := w.Stop(); got != 0 {
		t.Errorf("a second Stop = %d", got)
	}
}

func mib(n int64) string { return strconv.FormatInt(n>>20, 10) + " MiB" }
