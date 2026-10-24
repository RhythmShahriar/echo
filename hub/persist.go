// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package hub

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/RhythmShahriar/echo/model"
)

// The hub's inbox survives restarts in <store>/inbox.jsonl: one record per
// line, appended for every new message and again whenever one changes (the
// last record of an ID wins). Opening the file drops a torn last line,
// keeps the newest Keep messages and rewrites it compactly. The catcher's
// own persistent inbox (C13) will do this for every Echo; the hub can move
// to it then and keep only the site of each message here.

// record is one line of the inbox file.
type record struct {
	Site    string        `json:"site,omitempty"`
	Message model.Message `json:"message"`
}

// maxRecord bounds one line. Messages are capped well below this (bodies
// at 256 KiB a field), so a longer line is damage, not data.
const maxRecord = 64 << 20

type persister struct {
	mu    sync.Mutex
	path  string
	f     *os.File
	w     *bufio.Writer
	lines int
	keep  int
	err   error // the first write error, reported by close
}

func openPersister(dir string, keep int) (*persister, []record, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, err
	}
	p := &persister{path: filepath.Join(dir, "inbox.jsonl"), keep: keep}
	recs, err := load(p.path)
	if err != nil {
		return nil, nil, err
	}
	if len(recs) > keep {
		recs = recs[len(recs)-keep:]
	}
	if err := p.write(recs); err != nil {
		return nil, nil, err
	}
	return p, recs, nil
}

// load reads the file's records in the order their messages first
// appeared, each with its last state. Lines that don't parse (a write cut
// short by a crash) are skipped.
func load(path string) ([]record, error) {
	f, err := os.Open(path) //nolint:gosec // the hub's own store
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var recs []record
	at := map[string]int{}
	br := bufio.NewReaderSize(f, 64<<10)
	for {
		line, err := br.ReadBytes('\n')
		var r record
		if len(line) <= maxRecord && json.Unmarshal(line, &r) == nil && r.Message.ID != "" {
			if i, ok := at[r.Message.ID]; ok {
				recs[i] = r
			} else {
				at[r.Message.ID] = len(recs)
				recs = append(recs, r)
			}
		}
		if errors.Is(err, io.EOF) {
			return recs, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// write replaces the file with recs (through a temporary file, so a crash
// leaves the old one) and reopens it for appending.
func (p *persister) write(recs []record) error {
	tmp, err := os.CreateTemp(filepath.Dir(p.path), ".inbox-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	bw := bufio.NewWriter(tmp)
	for _, r := range recs {
		b, err := json.Marshal(r)
		if err != nil {
			_ = tmp.Close()
			return err
		}
		_, _ = bw.Write(append(b, '\n'))
	}
	if err := bw.Flush(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	if p.f != nil {
		_ = p.w.Flush()
		_ = p.f.Close()
	}
	if err := os.Rename(tmp.Name(), p.path); err != nil {
		return err
	}
	f, err := os.OpenFile(p.path, os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // the hub's own store
	if err != nil {
		return err
	}
	p.f, p.w, p.lines = f, bufio.NewWriter(f), len(recs)
	return nil
}

// append adds r and reports whether the file has grown to twice what is
// kept, so it should be compacted.
func (p *persister) append(r record) bool {
	b, err := json.Marshal(r)
	p.mu.Lock()
	defer p.mu.Unlock()
	if err == nil {
		_, err = p.w.Write(append(b, '\n'))
	}
	if err == nil {
		err = p.w.Flush() // a line at a time, so a crash loses at most the last one
	}
	if err != nil && p.err == nil {
		p.err = err
	}
	p.lines++
	return p.lines > 2*p.keep
}

func (p *persister) rewrite(recs []record) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.write(recs); err != nil && p.err == nil {
		p.err = err
	}
}

func (p *persister) close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.f == nil {
		return p.err
	}
	err := p.w.Flush()
	if cerr := p.f.Close(); err == nil {
		err = cerr
	}
	p.f = nil
	return errors.Join(p.err, err)
}
