// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build unix

package main

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/config"
)

// A canceled flush takes the shell's children with it: the sleep the
// shell started is gone soon after doctor returns.
func TestDoctorFlushKillsTheGroup(t *testing.T) {
	t.Chdir(t.TempDir())
	srv := httptest.NewServer(catcher.NewServer(catcher.NewInbox(10), catcher.Config{Version: "test"}))
	defer srv.Close()
	if err := os.MkdirAll(".echo", 0o700); err != nil {
		t.Fatal(err)
	}
	// The shell writes its PID, which is the group's ID; "; echo done"
	// keeps sh from exec'ing sleep, so sleep is a child in that group.
	flush := "smtp: \"off\"\nflush: [\"echo $$ > group.pid; sleep 25; echo done\"]\n"
	if err := os.WriteFile(config.Path(), []byte(flush), 0o600); err != nil {
		t.Fatal(err)
	}
	doctor, _ := lookup("doctor")
	p, err := parse(doctor, []string{"--url", srv.URL, "--plain"})
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	s := p.begin(&out, &errb)
	defer s.stop()
	ctx, cancel := context.WithCancel(s.ctx)
	s.ctx = ctx
	time.AfterFunc(300*time.Millisecond, cancel)
	if code := cmdDoctor(s); code != exitCanceled {
		t.Fatalf("exit %d, want %d: %s", code, exitCanceled, errb.String())
	}
	b, err := os.ReadFile("group.pid")
	if err != nil {
		t.Fatalf("the flush never started: %v", err)
	}
	pgid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		err := syscall.Kill(-pgid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			t.Fatalf("process group %d outlived the cancel (kill -0: %v)", pgid, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
