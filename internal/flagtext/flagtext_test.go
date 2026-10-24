// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package flagtext

import (
	"flag"
	"io"
	"testing"
	"time"
)

// TestError: the flag package's errors come out with flags spelled as
// the help spells them and say what a flag wants.
func TestError(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--bogus"}, "unknown flag --bogus"},
		{[]string{"-stroe"}, "unknown flag --stroe; did you mean --store?"},
		{[]string{"--store"}, "--store needs a value"},
		{[]string{"--keep", "x"}, `--keep "x": want a whole number`},
		{[]string{"--after-timeout", "soon"}, `--after-timeout "soon": want a duration like 10m or 5s`},
		{[]string{"--now=maybe"}, `--now "maybe": want true or false`},
	} {
		fs := flag.NewFlagSet("x", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		fs.String("store", "", "")
		fs.Int("keep", 0, "")
		fs.Duration("after-timeout", time.Minute, "")
		fs.Bool("now", false, "")
		err := fs.Parse(c.args)
		if err == nil {
			t.Fatalf("%q parsed", c.args)
		}
		if got := Error(err, fs); got != c.want {
			t.Errorf("%q: %q, want %q", c.args, got, c.want)
		}
	}
}

func TestClosest(t *testing.T) {
	if got := Closest("cmopare", []string{"compare", "run"}); got != "compare" {
		t.Errorf("a swap: %q", got)
	}
	if got := Closest("ab", []string{"up", "ls"}); got != "" {
		t.Errorf("a two-letter word: %q", got)
	}
}
