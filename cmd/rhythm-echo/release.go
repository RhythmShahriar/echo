// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/web"
	"github.com/RhythmShahriar/echo/wire"
)

// releaseAnswer is what the relay answers a release with.
type releaseAnswer struct {
	Released string `json:"released,omitempty"`
	To       string `json:"to,omitempty"`
	Log      string `json:"log,omitempty"`
	Error    string `json:"error,omitempty"`
}

// cmdRelease sends one caught email to one address through the relay
// that up runs with relay: in the config (integrations I8), as Echo
// Inbox's Release button does. The address must be on relay: allow.
// It exits 2 when the relay refuses the address or the message. It
// exits 3 when the relay's SMTP account didn't take it or Echo can't be
// reached.
func cmdRelease(s *session) int {
	if len(s.args) != 1 || s.o.to == "" {
		return s.fail(usageErr("usage: rhythm echo release <id> --to <address>   (IDs from: rhythm echo ls --channel email)"))
	}
	id := s.args[0]
	body, _ := json.Marshal(map[string]string{"to": s.o.to})
	path := strings.Replace(web.PathRelease, "{id}", url.PathEscape(id), 1)
	req, err := http.NewRequestWithContext(s.ctx, http.MethodPost, strings.TrimRight(s.echo.URL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return s.fail(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.echo.Token != "" {
		req.Header.Set(wire.TokenHeader, s.echo.Token)
	}
	// The relay may take up to 30 seconds to hand the email over.
	hc := *s.echo.HTTP
	hc.Timeout = 45 * time.Second
	resp, err := hc.Do(req)
	if err != nil {
		if s.ctx.Err() != nil {
			return s.fail(s.ctx.Err())
		}
		return s.fail(fmt.Errorf("%w at %s (start it with: rhythm echo up)", client.ErrNotRunning, s.echo.URL))
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	var a releaseAnswer
	jsonErr := json.Unmarshal(raw, &a)
	switch {
	case resp.StatusCode == http.StatusOK && jsonErr == nil:
		fmt.Fprintf(s.stdout, "Released %s to %s (logged as %s).\n", a.Released, a.To, a.Log)
		return exitOK
	case jsonErr != nil || a.Error == "":
		// Not the relay's answer: up runs without relay:, or an Echo that
		// has none answers for the catcher's routes.
		if resp.StatusCode == http.StatusUnauthorized {
			return s.fail(usageErr("Echo wants its token: set RHYTHM_ECHO_TOKEN or pass --token"))
		}
		return s.fail(usageErr("Echo at %s has no relay (it answered %s); add relay: to %s and start rhythm echo up again",
			s.echo.URL, resp.Status, s.configName()))
	case resp.StatusCode == http.StatusBadGateway:
		return s.fail(fmt.Errorf("%s", a.Error))
	}
	return s.fail(usageErr("%s", a.Error))
}
