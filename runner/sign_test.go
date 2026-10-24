// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package runner

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/wire"
)

func TestSignRunVector(t *testing.T) {
	// The K0 vector the Go, PHP and Node ports share.
	if got := signRun("secret", "r1", "checkout"); got != "r1/checkout.NhOp3HY0JQ76wSoPnn3sZg" {
		t.Errorf("signRun = %q", got)
	}
	if got := signRun("", "r1", "checkout"); got != "r1/checkout" {
		t.Errorf("no token: %q", got)
	}
}

// verify checks value the way a connector does (wire.VerifyRun), with
// code that shares nothing with signRun but the algorithm.
func verify(token, value string) (runAction string, ok bool) {
	i := strings.LastIndexByte(value, '.')
	if i < 0 {
		return "", false
	}
	m := hmac.New(sha256.New, []byte(token))
	m.Write([]byte(value[:i]))
	want := base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:16])
	return value[:i], hmac.Equal([]byte(value[i+1:]), []byte(want))
}

// With a token, the HTTP request (header and cookie) and the command's
// RHYTHM_ECHO_RUN carry a value that verifies with it; replays get the
// unsigned run to take apart. Without a token every value is unsigned,
// as before.
func TestRunValueSigned(t *testing.T) {
	for _, token := range []string{"", "s3cret-token"} {
		t.Run("token="+token, func(t *testing.T) {
			// Echo checks the token on its API, as `up` does beyond loopback.
			echo := catcher.NewServer(catcher.NewInbox(10), catcher.Config{Token: token})
			srv := httptest.NewServer(echo)
			defer srv.Close()
			var mu sync.Mutex
			var header, cookie string
			site := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				header = r.Header.Get(wire.RunHeader)
				if c, err := r.Cookie(wire.RunHeader); err == nil {
					cookie, _ = url.QueryUnescape(c.Value)
				}
			}))
			defer site.Close()
			envFile := filepath.Join(t.TempDir(), "env")
			var replayed string
			cfg := config.Default()
			cfg.Site = site.URL
			cfg.Quiet, cfg.Timeout = config.Duration{Duration: 20 * time.Millisecond}, config.Duration{Duration: 5 * time.Second}
			cfg.Actions = []config.Action{
				{Name: "Order", HTTP: &config.HTTPAction{URL: "/"}},
				{Name: "Cron", Command: `printf %s "$RHYTHM_ECHO_RUN" > '` + envFile + `'`},
				{Name: "Paid", Replay: &config.ReplayAction{Event: "x", To: "/hook"}},
			}
			run, err := Run(context.Background(), Options{Config: cfg, Client: client.New(srv.URL, token),
				Replay: func(_ context.Context, _ config.ReplayAction, _, run string) error { replayed = run; return nil }})
			if err != nil {
				t.Fatal(err)
			}
			checks := map[string]struct{ got, action string }{
				"header": {header, "action:order"},
				"cookie": {cookie, "action:order"},
			}
			// The command is written for sh; cmd has no printf.
			if runtime.GOOS != "windows" {
				env, _ := os.ReadFile(envFile)
				checks["RHYTHM_ECHO_RUN"] = struct{ got, action string }{string(env), "action:cron"}
			}
			for name, c := range checks {
				want := run.ID + "/" + c.action
				if token == "" {
					if c.got != want {
						t.Errorf("%s = %q, want %q", name, c.got, want)
					}
					continue
				}
				if v, ok := verify(token, c.got); !ok || v != want {
					t.Errorf("%s = %q doesn't verify as %q", name, c.got, want)
				}
				if _, ok := verify("another-token", c.got); ok {
					t.Errorf("%s verifies with another token", name)
				}
			}
			if replayed != run.ID+"/action:paid" {
				t.Errorf("replayer got %q", replayed)
			}
		})
	}
}
