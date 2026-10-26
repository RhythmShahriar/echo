// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package hwm measures one process's own peak resident memory, leaving
// out its children.
//
// The rusage of a waited child (ru_maxrss, what GNU time's %M prints)
// is the largest of that child and every descendant it reaped. Echo
// starts Chrome as its own child and reaps it, so that figure is
// Chrome's largest process, not Echo's. Linux keeps a per-process high
// water mark (VmHWM in /proc/<pid>/status) that counts the process
// alone; Watch polls it while the process runs. Elsewhere the peak is
// unknown (0).
package hwm

import (
	"bufio"
	"bytes"
	"strconv"
	"sync"
	"time"
)

// Interval is how often Watch reads the mark. The mark only grows, so a
// poll that misses the last moments loses at most the growth of one
// interval.
const Interval = 20 * time.Millisecond

// Watcher polls one process's high water mark until Stop.
type Watcher struct {
	pid  int
	stop chan struct{}
	done chan struct{}
	mu   sync.Mutex
	peak int64
}

// Watch starts polling pid's own peak resident memory.
func Watch(pid int) *Watcher {
	w := &Watcher{pid: pid, stop: make(chan struct{}), done: make(chan struct{})}
	go w.loop()
	return w
}

func (w *Watcher) loop() {
	defer close(w.done)
	t := time.NewTicker(Interval)
	defer t.Stop()
	for {
		w.read()
		select {
		case <-w.stop:
			return
		case <-t.C:
		}
	}
}

func (w *Watcher) read() {
	n, ok := Read(w.pid)
	if !ok {
		return
	}
	w.mu.Lock()
	w.peak = max(w.peak, n)
	w.mu.Unlock()
}

// Stop ends the polling and returns the largest mark seen, in bytes; 0
// when it was never readable (not Linux, or the process ended first).
// Call it after the process has exited: the last poll before the exit
// stands.
func (w *Watcher) Stop() int64 {
	select {
	case <-w.stop:
	default:
		close(w.stop)
	}
	<-w.done
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.peak
}

// Parse reads the VmHWM line of a /proc/<pid>/status file and returns
// it in bytes. A process that has exited (a zombie) has no such line.
func Parse(status []byte) (int64, bool) {
	sc := bufio.NewScanner(bytes.NewReader(status))
	for sc.Scan() {
		rest, found := bytes.CutPrefix(sc.Bytes(), []byte("VmHWM:"))
		if !found {
			continue
		}
		fields := bytes.Fields(rest)
		if len(fields) != 2 || string(fields[1]) != "kB" {
			return 0, false
		}
		kib, err := strconv.ParseInt(string(fields[0]), 10, 64)
		if err != nil || kib < 0 || kib > 1<<40 {
			return 0, false
		}
		return kib << 10, true
	}
	return 0, false
}
