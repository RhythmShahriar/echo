// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package catcher

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/model"
)

// DefaultKeep is how many messages an inbox keeps unless told otherwise.
const DefaultKeep = 10000

// InboxOptions configures OpenInbox.
type InboxOptions struct {
	// Dir is the folder the inbox is kept in, created owner-only (0700)
	// when missing and made owner-only when it isn't. Messages are masked
	// before they're stored yet still name real people and sites. The
	// folder must be Echo's alone: one that holds other files is refused
	// (see claim).
	Dir string
	// Keep is the most messages kept; default DefaultKeep.
	Keep int
	// KeepFor drops messages older than this, checked as messages arrive,
	// when the inbox opens and once a minute. 0 keeps them however old.
	KeepFor time.Duration
	// MaxBytes caps the message data kept; default DefaultMaxBytes.
	MaxBytes int64

	now         func() time.Time // time.Now; tests move it by hand
	segmentSize int64            // default segmentSize; tests make it small
}

// segmentSize is how large a journal file grows before Echo starts the
// next one.
const segmentSize = 16 << 20

// Journal record operations.
const (
	opAdd      = "add"
	opUpdate   = "update"
	opDelete   = "delete"
	opSnapshot = "snapshot" // first record of a compacted file: forget every file before it
)

// line is one record of the journal, one JSON object per line.
type line struct {
	Op string         `json:"op"`
	ID string         `json:"id,omitempty"` // delete
	M  *model.Message `json:"m,omitempty"`  // add and update: the whole message as it now is
}

// journal is a persisted inbox: append-only JSON Lines files named
// 000001.jsonl, 000002.jsonl and so on, oldest first. Every change is a
// new record (an update writes the whole message again) so a write never
// touches what's already on disk. A crash can cost at most the record
// being written: a torn last line, cut off on the next open. Once a file
// reaches segmentSize the next one starts. When the files hold more
// than twice what the messages need they're compacted into one.
type journal struct {
	dir         string
	f           *os.File // the newest file, appended to
	num         int      // its number
	size        int64    // its bytes
	total       int64    // bytes in every file
	live        int64    // bytes of the latest record of each message kept
	segmentSize int64
	err         error // the first write that failed; nothing is written after it
}

// OpenInbox opens the inbox kept in o.Dir (starting one there when there
// is none) and rebuilds it from what an earlier Echo wrote. Close it when done. A file
// that's damaged other than in its last line is an error rather than
// lost silently.
func OpenInbox(o InboxOptions) (*Inbox, error) {
	if o.Dir == "" {
		return nil, errors.New("open inbox: no folder given")
	}
	if err := claim(o.Dir); err != nil {
		return nil, err
	}
	in := NewInbox(cmp.Or(o.Keep, DefaultKeep))
	in.maxBytes = cmp.Or(o.MaxBytes, DefaultMaxBytes)
	in.keepFor = o.KeepFor
	if o.now != nil {
		in.now = o.now
	}
	j := &journal{dir: o.Dir, segmentSize: cmp.Or(o.segmentSize, segmentSize)}
	if err := in.replay(j); err != nil {
		return nil, err
	}
	in.mu.Lock()
	in.j = j
	in.evict() // the caps may be lower than the last Echo's; time has passed too
	if j.err == nil && j.total > 2*j.live+j.segmentSize {
		in.compact()
	}
	err := j.err
	in.mu.Unlock()
	if err != nil {
		_ = in.Close()
		return nil, err
	}
	in.stop = make(chan struct{})
	if in.keepFor > 0 {
		go in.expire(time.Minute, in.stop)
	}
	return in, nil
}

// expire drops expired messages every interval until Close, so an idle
// inbox doesn't keep them until the next message arrives.
func (in *Inbox) expire(interval time.Duration, stop <-chan struct{}) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			in.mu.Lock()
			if n := len(in.msgs); n > 0 {
				in.evict()
				if len(in.msgs) != n {
					in.notify()
				}
			}
			in.mu.Unlock()
		}
	}
}

// Persistent reports whether the inbox is kept on disk (OpenInbox).
func (in *Inbox) Persistent() bool {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.j != nil
}

// Err returns the first write to the inbox's folder that failed, after
// which nothing more is written to it. Nil for a memory inbox.
func (in *Inbox) Err() error {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.j == nil {
		return nil
	}
	return in.j.err
}

// Close flushes a persisted inbox to disk and closes its file. The inbox
// still works afterwards, in memory only. Closing a memory inbox does
// nothing.
func (in *Inbox) Close() error {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.stop != nil {
		close(in.stop)
		in.stop = nil
	}
	j := in.j
	if j == nil {
		return nil
	}
	in.j = nil
	if j.f == nil {
		return j.err
	}
	return errors.Join(j.err, j.f.Sync(), j.f.Close())
}

// write records a change to e in the journal. Called with in.mu held.
func (in *Inbox) write(op string, e *entry) {
	j := in.j
	if j == nil || j.err != nil {
		return
	}
	if op == opDelete {
		j.live -= e.disk
		j.append(line{Op: opDelete, ID: e.m.ID})
	} else {
		n := j.append(line{Op: op, M: &e.m})
		j.live += n - e.disk
		e.disk = n
	}
	if j.err == nil && j.size >= j.segmentSize {
		in.roll()
	}
}

// roll starts the next file, compacting instead when the files hold more
// than twice what the messages need. Called with in.mu held.
func (in *Inbox) roll() {
	j := in.j
	if j.total > 2*j.live {
		in.compact()
		return
	}
	if err := j.f.Close(); err != nil {
		j.err = err
		return
	}
	j.f = nil
	j.open(j.num+1, 0)
}

// compact writes every message kept into a new file that starts with a
// snapshot record, then removes the older files. Should Echo stop before
// they're gone, the snapshot record tells the next open to skip them.
// Called with in.mu held.
func (in *Inbox) compact() {
	j := in.j
	tmp := filepath.Join(j.dir, segmentName(j.num+1)+".tmp")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // a name Echo made in its own folder
	if err != nil {
		j.err = fmt.Errorf("compact inbox: %w", err)
		return
	}
	w := bufio.NewWriter(f)
	size, live := int64(0), int64(0)
	write := func(l line) int64 {
		b, err := json.Marshal(l)
		if err != nil {
			return 0
		}
		n, _ := w.Write(append(b, '\n'))
		return int64(n)
	}
	size += write(line{Op: opSnapshot})
	disk := make([]int64, len(in.msgs))
	for i, e := range in.msgs {
		disk[i] = write(line{Op: opAdd, M: &e.m})
		size += disk[i]
		live += disk[i]
	}
	err = errors.Join(w.Flush(), f.Sync(), f.Close())
	if err == nil {
		err = os.Rename(tmp, filepath.Join(j.dir, segmentName(j.num+1)))
	}
	if err != nil {
		_ = os.Remove(tmp)
		j.err = fmt.Errorf("compact inbox: %w", err)
		return
	}
	for i, e := range in.msgs {
		e.disk = disk[i]
	}
	if j.f != nil {
		_ = j.f.Close()
		j.f = nil
	}
	old := j.num
	j.total, j.live = 0, live
	j.open(old+1, size)
	removeBefore(j.dir, old+1) // should one stay, the snapshot record skips it on open
}

// removeBefore removes the journal files in dir numbered below num.
func removeBefore(dir string, num int) {
	nums, _ := segments(dir)
	for _, n := range nums {
		if n < num {
			_ = os.Remove(filepath.Join(dir, segmentName(n)))
		}
	}
}

// open makes file num, holding size bytes already, the one appended to.
func (j *journal) open(num int, size int64) {
	f, err := os.OpenFile(filepath.Join(j.dir, segmentName(num)), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		j.err = fmt.Errorf("inbox: %w", err)
		return
	}
	j.f, j.num, j.size = f, num, size
	j.total += size
}

// append writes l and returns its size.
func (j *journal) append(l line) int64 {
	b, err := json.Marshal(l)
	if err != nil {
		j.err = fmt.Errorf("inbox: %w", err)
		return 0
	}
	n, err := j.f.Write(append(b, '\n'))
	j.size += int64(n)
	j.total += int64(n)
	if err != nil {
		j.err = fmt.Errorf("inbox: %w", err)
	}
	return int64(n)
}

// marker names the file that marks a folder as an Echo inbox. Echo
// removes journal files it no longer needs, so it only does that in a
// folder it made its own: a --store pointed at a shared folder must not
// cost its owner a file that happens to look like a journal.
const marker = ".echo-inbox"

// claim makes dir an inbox folder: created owner-only when missing,
// tightened to owner-only when others may read it (messages are masked
// yet still name people) and marked as Echo's. A folder that holds other
// files and no marker is refused. One holding only journal files is an
// inbox from before the marker: it's marked and kept.
func claim(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("open inbox: %w", err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("open inbox: %w", err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // a folder needs x to be entered; 0700 is owner-only
			return fmt.Errorf("open inbox: make %s owner-only: %w", dir, err)
		}
	}
	mark := filepath.Join(dir, marker)
	if _, err := os.Stat(mark); err == nil {
		return nil
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("open inbox: %w", err)
	}
	for _, e := range ents {
		if !journalFile(e.Name()) {
			return fmt.Errorf("open inbox: %s holds files that aren't Echo's, such as %s; give the inbox a folder of its own", dir, e.Name())
		}
	}
	err = os.WriteFile(mark, []byte("This folder holds an Echo inbox. Echo removes the journal files in it that it no longer needs.\n"), 0o600)
	if err != nil {
		return fmt.Errorf("open inbox: %w", err)
	}
	return nil
}

// journalFile reports whether name is one Echo writes in an inbox folder:
// a journal file or a compaction in progress.
func journalFile(name string) bool {
	base, ok := strings.CutSuffix(strings.TrimSuffix(name, ".tmp"), ".jsonl")
	n, err := strconv.Atoi(base)
	return ok && err == nil && n > 0
}

func segmentName(n int) string { return fmt.Sprintf("%06d.jsonl", n) }

// segments returns the numbers of the journal files in dir, oldest first,
// removing compactions an earlier Echo didn't finish.
func segments(dir string) ([]int, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("open inbox: %w", err)
	}
	var nums []int
	for _, e := range ents {
		name := e.Name()
		if strings.HasSuffix(name, ".jsonl.tmp") {
			_ = os.Remove(filepath.Join(dir, name))
			continue
		}
		base, ok := strings.CutSuffix(name, ".jsonl")
		if n, err := strconv.Atoi(base); ok && err == nil && n > 0 && e.Type().IsRegular() {
			nums = append(nums, n)
		}
	}
	slices.Sort(nums)
	return nums, nil
}

// replay rebuilds the inbox from the journal's files and opens the newest
// to append to (a new one when there's none). A torn last line of the
// newest file, left by a crash mid-write, is cut off. Called before the
// inbox is shared.
func (in *Inbox) replay(j *journal) error {
	nums, err := segments(j.dir)
	if err != nil {
		return err
	}
	// Messages deleted during the replay are marked and dropped at the
	// end: removing each from the list as it goes made the replay
	// quadratic.
	var msgs []*entry
	byID := map[string][]*entry{} // oldest first, as several may share an ID
	dead := 0
	sizes := make([]int64, len(nums))
	from := 0 // the file the latest snapshot is in: older ones are left over
	for i, num := range nums {
		name := filepath.Join(j.dir, segmentName(num))
		size, err := readSegment(name, i == len(nums)-1, func(l line, n int64) {
			switch l.Op {
			case opSnapshot:
				msgs, byID, dead, j.live, from = nil, map[string][]*entry{}, 0, 0, i
			case opAdd:
				in.seq++
				e := &entry{seq: in.seq, m: *l.M, disk: n}
				msgs = append(msgs, e)
				byID[e.m.ID] = append(byID[e.m.ID], e)
				j.live += n
			case opUpdate:
				if es := byID[l.M.ID]; len(es) > 0 {
					e := es[len(es)-1] // Get and Update see the newest with an ID
					j.live += n - e.disk
					e.m, e.disk = *l.M, n
				}
			case opDelete:
				if es := byID[l.ID]; len(es) > 0 {
					j.live -= es[0].disk // evict and remove drop the oldest first
					es[0].seq = 0
					dead++
					if byID[l.ID] = es[1:]; len(es) == 1 {
						delete(byID, l.ID)
					}
				}
			}
		})
		if err != nil {
			return fmt.Errorf("open inbox: %s: %w (move the folder aside to start an empty inbox)", name, err)
		}
		sizes[i] = size
	}
	if from > 0 { // a compaction that stopped before removing the files it replaced
		removeBefore(j.dir, nums[from])
	}
	for i := from; i < len(nums)-1; i++ {
		j.total += sizes[i]
	}
	if dead > 0 {
		msgs = slices.DeleteFunc(msgs, func(e *entry) bool { return e.seq == 0 })
	}
	for _, e := range msgs {
		in.index[e.m.ID] = e // the newest with an ID wins, as in Add
		in.bytes += size(e.m)
	}
	in.msgs = msgs
	if len(nums) == 0 {
		j.open(1, 0)
	} else {
		j.open(nums[len(nums)-1], sizes[len(nums)-1])
	}
	return j.err
}

// errBadRecord is a line that isn't a record.
var errBadRecord = errors.New("not an inbox record")

// readSegment calls fn with every record of the file at name and its
// size. It returns how many bytes of the file hold records. In the
// newest file (last), a torn final line is cut off; in any file, a final
// line that doesn't read is skipped. A bad line anywhere else is an
// error.
func readSegment(name string, last bool, fn func(line, int64)) (int64, error) {
	f, err := os.OpenFile(name, os.O_RDWR, 0) //nolint:gosec // a file segments listed in the inbox folder
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReaderSize(f, 1<<20)
	var good int64 // bytes up to the end of the last good line
	for n := 1; ; n++ {
		b, err := r.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			if len(bytes.TrimSpace(b)) == 0 {
				break
			}
			// A line without its newline: the write was cut short.
			return truncate(f, good, last)
		}
		if err != nil {
			return good, err
		}
		l, perr := parseLine(b)
		if perr != nil {
			if _, err := r.Peek(1); errors.Is(err, io.EOF) {
				return truncate(f, good, last)
			}
			return good, fmt.Errorf("line %d: %w", n, perr)
		}
		fn(l, int64(len(b)))
		good += int64(len(b))
	}
	return good, nil
}

// truncate cuts a torn tail off the newest file, so the next record
// starts on a line of its own.
func truncate(f *os.File, good int64, last bool) (int64, error) {
	if !last {
		return good, nil
	}
	return good, f.Truncate(good)
}

func parseLine(b []byte) (line, error) {
	var l line
	if err := json.Unmarshal(b, &l); err != nil {
		return l, errBadRecord
	}
	switch {
	case l.Op == opAdd || l.Op == opUpdate:
		if l.M == nil || l.M.ID == "" {
			return l, errBadRecord
		}
	case l.Op == opDelete:
		if l.ID == "" {
			return l, errBadRecord
		}
	case l.Op != opSnapshot:
		return l, errBadRecord
	}
	return l, nil
}
