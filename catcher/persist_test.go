// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package catcher

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/wire"
)

func openInbox(t *testing.T, o InboxOptions) *Inbox {
	t.Helper()
	in, err := OpenInbox(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = in.Close() })
	return in
}

func closeInbox(t *testing.T, in *Inbox) {
	t.Helper()
	if err := in.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPersistedInboxSurvivesReopen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "inbox")
	in := openInbox(t, InboxOptions{Dir: dir})
	if !in.Persistent() || NewInbox(1).Persistent() {
		t.Fatal("Persistent is wrong")
	}
	for i := range 5 {
		in.Add(model.Message{Channel: model.Email, Subject: fmt.Sprint("order ", i), To: []string{"ann@example.com"}})
	}
	all := in.List(Filter{})
	in.Update(all[1].ID, func(m *model.Message) { m.Status, m.Error = model.Failed, "smtp said no" })
	in.Remove(all[3].ID)
	in.Begin("r1", "a1")
	tagged := in.Add(model.Message{Channel: model.Email, Subject: "during a1"})
	want := in.List(Filter{})
	closeInbox(t, in)

	re := openInbox(t, InboxOptions{Dir: dir})
	got := re.List(Filter{})
	if !slices.Equal(ids(got), ids(want)) {
		t.Fatalf("reopened %v, want %v", ids(got), ids(want))
	}
	for i := range got {
		if got[i].Subject != want[i].Subject || !got[i].At.Equal(want[i].At) {
			t.Errorf("message %d: %+v, want %+v", i, got[i], want[i])
		}
	}
	if m, _ := re.Get(all[1].ID); m.Status != model.Failed || m.Error != "smtp said no" {
		t.Errorf("update lost: %+v", m)
	}
	if m, _ := re.Get(tagged.ID); m.Run != "r1" || m.Action != "a1" {
		t.Errorf("attribution lost: %+v", m)
	}
	if re.Bytes() == 0 {
		t.Error("bytes not rebuilt")
	}
	// Paging needs the rebuilt order.
	page, next, err := re.Page(Filter{}, "", 2)
	if err != nil || len(page) != 2 || page[0].ID != tagged.ID || next == "" {
		t.Errorf("page after reopen: %v %q %v", ids(page), next, err)
	}
	// It keeps writing where it left off.
	added := re.Add(model.Message{Channel: model.SMS, Text: "after the restart"})
	closeInbox(t, re)
	again := openInbox(t, InboxOptions{Dir: dir})
	if got := again.List(Filter{}); len(got) != len(want)+1 || got[len(got)-1].ID != added.ID {
		t.Errorf("second reopen: %v", ids(got))
	}
}

func TestPersistedInboxFilesAreOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows files have no Unix mode")
	}
	dir := filepath.Join(t.TempDir(), "inbox")
	in := openInbox(t, InboxOptions{Dir: dir})
	in.Add(model.Message{Channel: model.Email, Subject: "hello"})
	closeInbox(t, in)
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("folder: %v %v", fi.Mode(), err)
	}
	fi, err := os.Stat(filepath.Join(dir, "000001.jsonl"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file: %v %v", fi, err)
	}
}

func TestPersistedInboxDoesNotKeepCodes(t *testing.T) {
	dir := t.TempDir()
	in := openInbox(t, InboxOptions{Dir: dir})
	s := NewServer(in, Config{})
	srv := httptest.NewServer(s)
	defer srv.Close()
	post(t, srv, wire.Capture{Version: 1, Kind: "email", Email: &wire.Email{To: []string{"ann@example.com"},
		Subject: "Your code", Text: "Your verification code is 482913"}})
	closeInbox(t, in)
	b, err := os.ReadFile(filepath.Join(dir, "000001.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "482913") {
		t.Fatalf("the code reached the disk: %s", b)
	}
}

func TestPersistedInboxRetention(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	dir := t.TempDir()
	in := openInbox(t, InboxOptions{Dir: dir, Keep: 3, KeepFor: time.Hour, now: clock})
	for i := range 5 {
		in.Add(model.Message{Channel: model.Email, Subject: fmt.Sprint(i)})
		now = now.Add(10 * time.Minute)
	}
	subjects := func(in *Inbox) []string {
		var out []string
		for _, m := range in.List(Filter{}) {
			out = append(out, m.Subject)
		}
		return out
	}
	if got := subjects(in); !slices.Equal(got, []string{"2", "3", "4"}) {
		t.Fatalf("by count: %v", got)
	}
	closeInbox(t, in)

	// 12:20 to 12:40 were kept; at 13:35 only 12:40 is under an hour old.
	now = time.Date(2026, 10, 1, 13, 35, 0, 0, time.UTC)
	in = openInbox(t, InboxOptions{Dir: dir, Keep: 3, KeepFor: time.Hour, now: clock})
	if got := subjects(in); !slices.Equal(got, []string{"4"}) {
		t.Fatalf("by age on open: %v", got)
	}
	// An arrival drops what has expired since.
	now = now.Add(20 * time.Minute)
	in.Add(model.Message{Channel: model.Email, Subject: "5"})
	if got := subjects(in); !slices.Equal(got, []string{"5"}) {
		t.Fatalf("by age on add: %v", got)
	}
	closeInbox(t, in)
	in = openInbox(t, InboxOptions{Dir: dir, now: clock})
	if got := subjects(in); !slices.Equal(got, []string{"5"}) {
		t.Fatalf("drops were not written: %v", got)
	}

	// A lower count cap on the next open applies at once.
	for i := range 4 {
		in.Add(model.Message{Channel: model.Email, Subject: fmt.Sprint(6 + i)})
	}
	closeInbox(t, in)
	in = openInbox(t, InboxOptions{Dir: dir, Keep: 2, now: clock})
	if got := subjects(in); !slices.Equal(got, []string{"8", "9"}) {
		t.Fatalf("lower cap on open: %v", got)
	}
}

func TestMemoryInboxExpiresByAge(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	in := NewInbox(10)
	in.now = func() time.Time { return now }
	in.keepFor = time.Hour
	in.Add(model.Message{Channel: model.Email, Subject: "old"})
	now = now.Add(2 * time.Hour)
	in.Add(model.Message{Channel: model.Email, Subject: "new"})
	if got := in.List(Filter{}); len(got) != 1 || got[0].Subject != "new" {
		t.Fatalf("got %v", got)
	}
}

func TestPersistedInboxRecoversATornTail(t *testing.T) {
	for name, tail := range map[string]string{
		"cut mid-record":      `{"op":"add","m":{"id":"zz","chan`,
		"bad last line":       "{\"op\":\"add\",\"m\":{\"id\":\"zz\"\n",
		"garbage last line":   "\x00\x00\x00\n",
		"unknown op":          "{\"op\":\"rename\"}\n",
		"newline-only tail":   "\n",
		"spaces without line": "   ",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			in := openInbox(t, InboxOptions{Dir: dir})
			a := in.Add(model.Message{Channel: model.Email, Subject: "a"})
			b := in.Add(model.Message{Channel: model.Email, Subject: "b"})
			closeInbox(t, in)
			file := filepath.Join(dir, "000001.jsonl")
			f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = f.WriteString(tail)
			_ = f.Close()

			in = openInbox(t, InboxOptions{Dir: dir})
			if got := ids(in.List(Filter{})); !slices.Equal(got, []string{a.ID, b.ID}) {
				t.Fatalf("got %v", got)
			}
			c := in.Add(model.Message{Channel: model.Email, Subject: "c"})
			closeInbox(t, in)
			in = openInbox(t, InboxOptions{Dir: dir})
			if got := ids(in.List(Filter{})); !slices.Equal(got, []string{a.ID, b.ID, c.ID}) {
				t.Fatalf("after writing past the tail: %v", got)
			}
		})
	}
}

func TestPersistedInboxRefusesDamageInTheMiddle(t *testing.T) {
	dir := t.TempDir()
	in := openInbox(t, InboxOptions{Dir: dir})
	in.Add(model.Message{Channel: model.Email, Subject: "a"})
	closeInbox(t, in)
	file := filepath.Join(dir, "000001.jsonl")
	b, _ := os.ReadFile(file)
	if err := os.WriteFile(file, append([]byte("not json\n"), b...), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := OpenInbox(InboxOptions{Dir: dir})
	if err == nil || !strings.Contains(err.Error(), "line 1: not an inbox record") || !strings.Contains(err.Error(), "move the folder aside") {
		t.Fatalf("err = %v", err)
	}
	if _, err := OpenInbox(InboxOptions{}); err == nil {
		t.Fatal("no folder: want an error")
	}
}

func TestPersistedInboxRollsAndCompacts(t *testing.T) {
	dir := t.TempDir()
	o := InboxOptions{Dir: dir, Keep: 5, segmentSize: 2 << 10}
	in := openInbox(t, o)
	var last model.Message
	for i := range 200 {
		last = in.Add(model.Message{Channel: model.Email, Subject: fmt.Sprint("message ", i), Text: strings.Repeat("x", 100)})
		in.Update(last.ID, func(m *model.Message) { m.Status = model.Sent })
	}
	want := ids(in.List(Filter{}))
	closeInbox(t, in)
	nums, err := segments(dir)
	if err != nil {
		t.Fatal(err)
	}
	// 200 messages written twice is about 80 KiB: without compaction
	// that's 40 files of 2 KiB.
	if len(nums) > 3 {
		t.Fatalf("%d files left: compaction didn't run", len(nums))
	}
	in = openInbox(t, o)
	if got := ids(in.List(Filter{})); !slices.Equal(got, want) || got[len(got)-1] != last.ID {
		t.Fatalf("after compaction: %v, want %v", got, want)
	}
	if m, _ := in.Get(last.ID); m.Status != model.Sent {
		t.Fatalf("update lost in compaction: %+v", m)
	}
}

func TestPersistedInboxSkipsFilesAnUnfinishedCompactionLeft(t *testing.T) {
	dir := t.TempDir()
	in := openInbox(t, InboxOptions{Dir: dir})
	old := in.Add(model.Message{Channel: model.Email, Subject: "replaced"})
	closeInbox(t, in)
	// A compaction that wrote 000002 but stopped before removing 000001,
	// and one that never finished its temporary file.
	kept := `{"op":"add","m":{"id":"kept1","channel":"email","subject":"kept","status":"","source":{"via":""},"at":"2026-10-01T12:00:00Z"}}`
	if err := os.WriteFile(filepath.Join(dir, "000002.jsonl"), []byte("{\"op\":\"snapshot\"}\n"+kept+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "000003.jsonl.tmp"), []byte("{\"op\":\"snapshot\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	in = openInbox(t, InboxOptions{Dir: dir})
	if got := ids(in.List(Filter{})); !slices.Equal(got, []string{"kept1"}) {
		t.Fatalf("got %v (old %s)", got, old.ID)
	}
	closeInbox(t, in)
	ents, _ := os.ReadDir(dir)
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	if !slices.Equal(names, []string{marker, "000002.jsonl"}) {
		t.Fatalf("files left: %v", names)
	}
}

func TestHealthNamesThePersistedStore(t *testing.T) {
	in := openInbox(t, InboxOptions{Dir: t.TempDir()})
	if s := NewServer(in, Config{}); s.cfg.Store != "jsonl" {
		t.Fatalf("store = %q", s.cfg.Store)
	}
	if s := NewServer(NewInbox(1), Config{}); s.cfg.Store != "memory" {
		t.Fatalf("store = %q", s.cfg.Store)
	}
}

func TestPersistedInboxReopensTenThousandQuickly(t *testing.T) {
	if testing.Short() {
		t.Skip("writes 10,000 messages")
	}
	dir := t.TempDir()
	in := openInbox(t, InboxOptions{Dir: dir})
	for i := range DefaultKeep {
		in.Add(model.Message{Channel: model.Email, From: "shop@shop.test", To: []string{"ann@example.com"},
			Subject: fmt.Sprint("Order ", i, " shipped"), Text: strings.Repeat("Thanks for your order. ", 20),
			Headers: map[string]string{"message-id": fmt.Sprint("<", i, "@shop.test>")}})
	}
	closeInbox(t, in)
	start := time.Now()
	in = openInbox(t, InboxOptions{Dir: dir})
	took := time.Since(start)
	if n := len(in.List(Filter{})); n != DefaultKeep {
		t.Fatalf("reopened %d", n)
	}
	if took > time.Second*slowdown {
		t.Fatalf("reopening took %v", took)
	}
	t.Logf("reopened %d messages in %v", DefaultKeep, took)
}

func TestInboxFolderMustBeEchos(t *testing.T) {
	dir := t.TempDir()
	notes := filepath.Join(dir, "000001.jsonl.tmp.notes")
	for _, f := range []string{notes, filepath.Join(dir, "000007.jsonl")} {
		if err := os.WriteFile(f, []byte("someone else's\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := OpenInbox(InboxOptions{Dir: dir}); err == nil || !strings.Contains(err.Error(), "a folder of its own") {
		t.Fatalf("a shared folder opened: %v", err)
	}
	for _, f := range []string{notes, filepath.Join(dir, "000007.jsonl"), filepath.Join(dir, marker)} {
		if _, err := os.Stat(f); (err == nil) != (f != filepath.Join(dir, marker)) {
			t.Errorf("%s: %v", filepath.Base(f), err)
		}
	}
}

func TestInboxFromBeforeTheMarkerOpens(t *testing.T) {
	dir := t.TempDir()
	in := openInbox(t, InboxOptions{Dir: dir})
	m := in.Add(model.Message{Channel: model.Email, Subject: "kept"})
	closeInbox(t, in)
	if err := os.Remove(filepath.Join(dir, marker)); err != nil {
		t.Fatal(err)
	}
	in = openInbox(t, InboxOptions{Dir: dir})
	if _, ok := in.Get(m.ID); !ok {
		t.Error("the message is gone")
	}
	if _, err := os.Stat(filepath.Join(dir, marker)); err != nil {
		t.Errorf("not marked: %v", err)
	}
}

func TestInboxFolderIsMadeOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows folders have no Unix mode")
	}
	dir := filepath.Join(t.TempDir(), "inbox")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil { // past the umask
		t.Fatal(err)
	}
	openInbox(t, InboxOptions{Dir: dir})
	fi, err := os.Stat(dir)
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("mode %v, %v; want 0700", fi.Mode().Perm(), err)
	}
}

func TestRefusedInboxFolderKeepsItsMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows folders have no Unix mode")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenInbox(InboxOptions{Dir: dir}); err == nil || !strings.Contains(err.Error(), "aren't Echo's") {
		t.Fatalf("err = %v; want the folder refused", err)
	}
	fi, err := os.Stat(dir)
	if err != nil || fi.Mode().Perm() != 0o755 {
		t.Errorf("mode %v, %v; want 0755 kept", fi.Mode().Perm(), err)
	}
}
