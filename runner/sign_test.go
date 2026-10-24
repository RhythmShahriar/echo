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
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/model"
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

// With a run key, the HTTP request (header and cookie) and the command's
// RHYTHM_ECHO_RUN carry a value that verifies with it; replays get the
// unsigned run to take apart. Without one every value is unsigned, as
// before, even when Echo has a token: until Echo strips signatures a
// signed value would leave the mail with no action.
func TestRunValueSigned(t *testing.T) {
	for _, tc := range []struct{ token, key string }{{"", ""}, {"s3cret-token", ""}, {"s3cret-token", "s3cret-token"}} {
		token := tc.token
		t.Run("token="+token+",key="+tc.key, func(t *testing.T) {
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
			run, err := Run(context.Background(), Options{Config: cfg, Client: client.New(srv.URL, token), RunKey: tc.key,
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
				if tc.key == "" {
					if c.got != want {
						t.Errorf("%s = %q, want %q", name, c.got, want)
					}
					continue
				}
				if v, ok := verify(tc.key, c.got); !ok || v != want {
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

// With a token on Echo and no run key (the default), mail from every
// kind of action is still credited to it: the site tags it with the
// unsigned run value, which an Echo that doesn't strip signatures takes
// as the action ID.
func TestTokenKeepsAttribution(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("no curl")
	}
	if runtime.GOOS == "windows" {
		t.Skip("the command is written for sh")
	}
	const token = "tok"
	srv := httptest.NewServer(catcher.NewServer(catcher.NewInbox(10), catcher.Config{Token: token}))
	defer srv.Close()
	echo := client.New(srv.URL, token)
	site := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = echo.Capture(r.Context(), wire.Capture{Version: 1, Kind: "email", Run: r.Header.Get(wire.RunHeader),
			Source: wire.Source{Via: "test"}, Email: &wire.Email{To: []string{"a@shop.test"}, Subject: "Order", Text: "Thanks"}})
	}))
	defer site.Close()
	cfg := config.Default()
	cfg.Site = site.URL
	cfg.Quiet, cfg.Timeout = config.Duration{Duration: 100 * time.Millisecond}, config.Duration{Duration: 3 * time.Second}
	expect := config.Expect{model.Email: {N: 1}}
	cfg.Actions = []config.Action{
		{Name: "Order", HTTP: &config.HTTPAction{URL: "/"}, Expect: expect},
		{Name: "Cron", Command: `curl -s -X POST -H "` + wire.TokenHeader + `: ` + token + `" ` + srv.URL + `/v1/capture -d "{\"v\":1,\"kind\":\"email\",\"run\":\"$RHYTHM_ECHO_RUN\",\"source\":{\"via\":\"test\"},\"email\":{\"to\":[\"ops@shop.test\"],\"subject\":\"Cron\",\"text\":\"Done\"}}" >/dev/null`, Expect: expect},
	}
	run, err := Run(context.Background(), Options{Config: cfg, Client: echo})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range run.Actions {
		if len(a.Messages) != 1 || len(a.Issues) != 0 || a.Error != "" {
			t.Errorf("%s: %d messages, issues %+v, error %q", a.Label, len(a.Messages), a.Issues, a.Error)
		}
	}
}
