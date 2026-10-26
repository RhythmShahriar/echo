// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package server

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// Redirect lengths of the redirect-chain fixture: Echo follows up to 10
// hops (PRD 13.6), so the good state stays under it and the broken one
// goes past it.
const (
	goodHops   = 3
	brokenHops = 12
)

// SessionCookie is the first-party session cookie of the brochure site
// g12: not a tracker, so it must never make Echo ask for consent.
const SessionCookie = "shop_session"

// special handles what a template can't: headers and redirects. It
// reports whether it answered the request.
func (s *Server) special(w http.ResponseWriter, r *http.Request, id string, broken bool) bool {
	p := r.URL.Path
	switch id {
	case "g12":
		if _, err := r.Cookie(SessionCookie); err != nil {
			// Not Secure: in Go mode the site may be served over plain http.
			c := &http.Cookie{Name: SessionCookie, Value: "tbsession1", Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode} //nolint:gosec // see above
			http.SetCookie(w, c)
		}
	case "redirect-chain":
		hops := goodHops
		if broken {
			hops = brokenHops
		}
		if p == "/" {
			http.Redirect(w, r, "/r/1", http.StatusFound)
			return true
		}
		if n, ok := strings.CutPrefix(p, "/r/"); ok {
			i, err := strconv.Atoi(n)
			if err != nil || i < 1 {
				http.NotFound(w, r)
				return true
			}
			next := "/landed/"
			if i < hops-1 {
				next = fmt.Sprintf("/r/%d", i+1)
			}
			http.Redirect(w, r, next, http.StatusFound)
			return true
		}
	case "redirect-loop":
		if !broken {
			return false
		}
		switch p {
		case "/":
			http.Redirect(w, r, "/loop/", http.StatusFound)
			return true
		case "/loop/":
			http.Redirect(w, r, "/", http.StatusFound)
			return true
		}
	}
	return false
}
