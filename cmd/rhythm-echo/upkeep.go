// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"cmp"
	"context"
	"crypto/subtle"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/check"
	"github.com/RhythmShahriar/echo/compat"
	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/hub"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/relay"
	"github.com/RhythmShahriar/echo/web"
	"github.com/RhythmShahriar/echo/wire"
)

// inboxStore is where up keeps what it catches: dir "" is memory only.
type inboxStore struct {
	dir     string
	keep    int
	keepFor time.Duration
	from    string // what set dir, for messages: --store or the config's inbox: store:
}

// inboxSettings reads the config's inbox: and up's --store, --keep and
// --keep-for, the flags winning. A relative store: in the config is
// relative to the project's folder, as the config's other paths are.
func inboxSettings(s *session) (inboxStore, error) {
	var st inboxStore
	if in := s.cfg.Inbox; in != nil {
		if in.Store != "" && in.Store != "memory" {
			st.dir, st.from = in.Store, "inbox: store: in "+s.configName()
			if !filepath.IsAbs(st.dir) {
				st.dir = filepath.Join(s.projectDir(), st.dir)
			}
		}
		st.keep, st.keepFor = in.Keep, in.KeepFor.Duration
	}
	if s.o.storeDir != "" {
		st.dir, st.from = s.o.storeDir, "--store"
	}
	switch {
	case s.o.keep < 0:
		return st, usageErr("--keep %d: give how many messages to keep, e.g. 50000", s.o.keep)
	case s.o.keepFor < 0:
		return st, usageErr("--keep-for %s: give how long to keep messages, e.g. 14d", shortDuration(s.o.keepFor))
	}
	if s.o.keep > 0 {
		st.keep = s.o.keep
	}
	if s.o.keepFor > 0 {
		st.keepFor = s.o.keepFor
	}
	if st.keepFor > 0 && st.dir == "" {
		return st, usageErr("--keep-for and keep_for: need a folder to keep messages in: --store <dir> or inbox: store: in %s", s.configName())
	}
	return st, nil
}

// openInbox opens the inbox st describes.
func openInbox(st inboxStore) (*catcher.Inbox, error) {
	if st.dir == "" {
		return catcher.NewInbox(cmp.Or(st.keep, catcher.DefaultKeep)), nil
	}
	in, err := catcher.OpenInbox(catcher.InboxOptions{Dir: st.dir, Keep: st.keep, KeepFor: st.keepFor})
	if err != nil {
		return nil, fmt.Errorf("keeping the inbox in %s (%s): %w", st.dir, st.from, err)
	}
	return in, nil
}

// inboxLine is the banner step for a kept inbox.
func inboxLine(st inboxStore, n int) string {
	line := fmt.Sprintf("%s  (kept across restarts; %s in it now", st.dir, plural(n, "message"))
	if st.keepFor > 0 {
		line += ", each kept " + days(st.keepFor)
	}
	return line + ")"
}

// days writes d in days when it is whole days (14d), as keep_for: is
// usually given, and as config.Short does otherwise.
func days(d time.Duration) string {
	if d >= 24*time.Hour && d%(24*time.Hour) == 0 {
		return strconv.Itoa(int(d/(24*time.Hour))) + "d"
	}
	return config.Short(d)
}

// upSites are the per-site write tokens of up --sites.
type upSites struct {
	read  string
	write map[string]string // token → site ID
	ids   []string
	env   string // the read token's variable
}

// loadSites reads --sites: a sites.yaml as rhythm echo hub reads it. The
// read token comes from the variable it names (RHYTHM_ECHO_TOKEN unless
// read_token_env says otherwise), so --token can't say another one.
func loadSites(s *session) (*upSites, error) {
	if s.o.sites == "" {
		return nil, nil
	}
	ss, err := hub.LoadSites(s.o.sites, os.Getenv)
	if err != nil {
		return nil, usageErr("--sites: %v", err)
	}
	if s.o.token != "" && s.o.token != ss.ReadToken() {
		return nil, usageErr("--token: with --sites the read token is %s's; leave --token off", ss.ReadTokenEnv)
	}
	u := &upSites{read: ss.ReadToken(), write: ss.WriteTokens(), env: ss.ReadTokenEnv}
	for _, site := range ss.Sites {
		u.ids = append(u.ids, site.ID)
	}
	return u, nil
}

// tokens are every token up takes: the read token, then the sites'.
func (u *upSites) tokens() []string {
	return append([]string{u.read}, slices.Sorted(maps.Keys(u.write))...)
}

// line is the banner step for --sites.
func (u *upSites) line() string {
	return fmt.Sprintf("%s: each sends with its own write token (captures only); the read token (%s) reads them all",
		strings.Join(u.ids, ", "), u.env)
}

// writeTokens sends a request that carries a site's write token straight
// to the catcher, which lets it send captures and mark actions only
// (catcher.Config.WriteTokens). Anything else goes to next, where the
// read token is needed.
func writeTokens(tokens []string, srv http.Handler, next http.Handler) http.Handler {
	writes := tokens[1:]
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, match := r.Header.Get(wire.TokenHeader), false
		for _, t := range writes {
			if got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(t)) == 1 {
				match = true
			}
		}
		if match {
			srv.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// relayEnv holds the relay's SMTP account URL, which has a password in
// it, so it is better kept out of a committed config.
const relayEnv = "RHYTHM_ECHO_RELAY"

// releasePattern is the route a reader releases one caught email on.
const releasePattern = "POST /v1/messages/{id}/release"

// releaseRoute is the relay's route (integrations I8) when the config
// has relay:, with the banner line that says where releases go. A
// release needs Echo's token or a viewer's sign-in, so up refuses a relay
// it could never use.
func releaseRoute(s *session, inbox *catcher.Inbox, token string, login *web.Login) (*upRoute, string, error) {
	rc := s.cfg.Relay
	if rc == nil {
		return nil, "", nil
	}
	raw := cmp.Or(os.Getenv(relayEnv), rc.URL)
	switch {
	case raw == "":
		return nil, "", usageErr("relay: needs the SMTP account to send through: set %s to smtp://user:pass@host:587 (or url: under relay: in %s)", relayEnv, s.configName())
	case token == "" && login == nil:
		return nil, "", usageErr("relay: releasing needs Echo's token or a viewer login; set --token (or RHYTHM_ECHO_TOKEN) or --login")
	case len(rc.Allow) == 0:
		return nil, "", usageErr("relay: allow: lists no address, so nothing could be released; add the client's, e.g. \"*@client.example\"")
	}
	c := relay.Config{URL: raw, Allow: rc.Allow, ReadToken: token}
	if login != nil {
		c.Session = login.Session
	}
	h, err := relay.Handler(inbox, c)
	if err != nil {
		return nil, "", usageErr("%v", err)
	}
	var hosts []string
	if host, _, err := net.SplitHostPort(s.cfg.Listen); err == nil {
		if ip := net.ParseIP(host); host != "" && (ip == nil || !ip.IsUnspecified()) {
			hosts = []string{host}
		}
	}
	h = catcher.Protect(h, catcher.ProtectOptions{Token: token, Hosts: hosts})
	where := "the relay"
	if u, err := url.Parse(raw); err == nil {
		where = u.Host // never the user or password
	}
	line := fmt.Sprintf("releases go out through %s, only to %s", where, strings.Join(rc.Allow, ", "))
	return &upRoute{pattern: releasePattern, probe: "/v1/messages/m1/release", handler: h, session: true}, line, nil
}

// spamScorer asks spamd to score each new email (up --spamassassin) in
// the background, never holding up the SMTP sink or a connector, and
// keeps the verdict on the message for the compat/spam check.
type spamScorer struct {
	addr  string
	inbox *catcher.Inbox
	slots chan struct{} // at most this many checks at once
	wg    sync.WaitGroup
	mu    sync.Mutex
	asked map[string]bool
}

// newSpamScorer returns nil when addr is "": scoring is off.
func newSpamScorer(addr string, inbox *catcher.Inbox) *spamScorer {
	if addr == "" {
		return nil
	}
	return &spamScorer{addr: addr, inbox: inbox, slots: make(chan struct{}, 4), asked: map[string]bool{}}
}

// score scores m once, when it is an email that has no verdict yet.
func (sc *spamScorer) score(m model.Message) {
	if sc == nil || m.Channel != model.Email || m.Headers[check.SpamHeader] != "" {
		return
	}
	sc.mu.Lock()
	if sc.asked[m.ID] {
		sc.mu.Unlock()
		return
	}
	if len(sc.asked) > 2*catcher.DefaultKeep {
		clear(sc.asked)
	}
	sc.asked[m.ID] = true
	sc.mu.Unlock()
	sc.wg.Add(1)
	go func() {
		defer sc.wg.Done()
		sc.slots <- struct{}{}
		defer func() { <-sc.slots }()
		res, err := compat.SpamCheck(context.Background(), sc.addr, relay.Message(m))
		value := check.SpamValue(res, err)
		sc.inbox.Update(m.ID, func(mm *model.Message) {
			if mm.Headers == nil {
				mm.Headers = map[string]string{}
			}
			mm.Headers[check.SpamHeader] = value
		})
	}()
}

// wait waits for the checks under way, for a clean stop.
func (sc *spamScorer) wait() {
	if sc != nil {
		sc.wg.Wait()
	}
}

// hostPort reports whether addr is a host and a port from 1 to 65535.
func hostPort(addr string) bool {
	host, port, err := net.SplitHostPort(addr)
	n, nerr := strconv.Atoi(port)
	return err == nil && host != "" && nerr == nil && n > 0 && n < 65536
}

// spamAddr adds spamd's port to an address without one.
func spamAddr(addr string) string {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return net.JoinHostPort(strings.Trim(addr, "[]"), compat.SpamPort)
	}
	return addr
}
