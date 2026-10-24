package main

import (
	"log/slog"
	"testing"
)

func TestViewHandlerShowsWarnings(t *testing.T) {
	var got []string
	l := slog.New(viewHandler{log: func(s string) { got = append(got, s) }})
	l.Debug("tunnel", "host", "a.test")
	l.With("via", "connect").Warn("refused a tunnel", "host", "api.stripe.com")
	if len(got) != 1 || got[0] != "proxy: refused a tunnel via=connect host=api.stripe.com" {
		t.Errorf("got %q", got)
	}
}
