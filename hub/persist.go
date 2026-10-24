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

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/model"
)

// An earlier hub kept its inbox in <store>/inbox.jsonl, one record per
// line with the site beside the message (the last record of an ID wins).
// The hub now keeps it in the catcher's persistent inbox (<store>/inbox)
// with the site on the message (Source.Site). migrate moves the old file
// into it once.

// legacyInbox is the earlier hub's inbox file in the store.
const legacyInbox = "inbox.jsonl"

// record is one line of the old inbox file.
type record struct {
	Site    string        `json:"site,omitempty"`
	Message model.Message `json:"message"`
}

// maxRecord bounds one line. Messages are capped well below this (bodies
// at 256 KiB a field), so a longer line is damage, not data.
const maxRecord = 64 << 20

// migrate adds the messages of an old inbox.jsonl in store to in, each
// marked with its site, and removes the file. The messages keep their
// IDs, times and runs. An inbox that already holds messages was migrated
// before, so only the file goes then.
func migrate(store string, in *catcher.Inbox) error {
	path := filepath.Join(store, legacyInbox)
	recs, err := load(path)
	if err != nil || recs == nil {
		return err
	}
	if len(in.List(catcher.Filter{})) == 0 {
		for _, r := range recs {
			m := r.Message
			if m.Source.Site == "" {
				m.Source.Site = r.Site
			}
			in.Add(m)
		}
	}
	return os.Remove(path)
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
