// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Login paths and the session cookie.
const (
	PathLogin     = "/login"  // GET: the sign-in page; POST: sign in
	PathLogout    = "/logout" // POST: sign out
	SessionCookie = "echo_session"
)

// SessionTTL is how long a sign-in lasts.
const SessionTTL = 8 * time.Hour

// maxFailures is how many wrong passwords Login takes per minute, from
// everyone together, before it refuses every attempt (a right password
// too) until the oldest failure is a minute old. Counting everyone keeps
// a guesser from spreading attempts over many addresses; the cost is
// that a guesser also locks the real viewers out for that minute.
const maxFailures = 20

// Login lets people who only view the inbox (an agency's client signing
// off a change) sign in with a user name and password instead of Echo's
// token. A sign-in is a random session ID in an HttpOnly, SameSite=Strict
// cookie that lasts SessionTTL. It grants reading only: Guard refuses
// anything but GET and HEAD.
type Login struct {
	user, pass [32]byte // SHA-256, so comparing takes the same time whatever the lengths
	name       string

	mu       sync.Mutex
	sessions map[string]time.Time // session ID → expiry
	failures []time.Time          // wrong passwords in the last minute
	now      func() time.Time
}

// ParseLogin reads "user:password". The password may hold colons; it
// must be at least 8 characters.
func ParseLogin(spec string) (*Login, error) {
	user, pass, ok := strings.Cut(spec, ":")
	switch {
	case !ok || user == "" || pass == "":
		return nil, errors.New("want user:password")
	case len(pass) < 8:
		return nil, errors.New("the password must be at least 8 characters")
	}
	return &Login{user: sha256.Sum256([]byte(user)), pass: sha256.Sum256([]byte(pass)), name: user,
		sessions: map[string]time.Time{}, now: time.Now}, nil
}

// User is the user name viewers sign in with.
func (l *Login) User() string { return l.name }

// check compares a user name and password in constant time. Both are
// always compared; a hash hides their lengths.
func (l *Login) check(user, pass string) bool {
	u, p := sha256.Sum256([]byte(user)), sha256.Sum256([]byte(pass))
	return subtle.ConstantTimeCompare(u[:], l.user[:])&subtle.ConstantTimeCompare(p[:], l.pass[:]) == 1
}

// throttled reports whether the last minute saw maxFailures wrong
// passwords. It is asked before the password is checked: refusing only
// the wrong ones would still let a guesser in on the right one.
func (l *Login) throttled() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := l.now().Add(-time.Minute)
	kept := l.failures[:0]
	for _, t := range l.failures {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	l.failures = kept
	return len(l.failures) >= maxFailures
}

// failed records a wrong password.
func (l *Login) failed() {
	l.mu.Lock()
	l.failures = append(l.failures, l.now())
	l.mu.Unlock()
}

func (l *Login) start() (string, time.Time) {
	id := rand.Text()
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for k, exp := range l.sessions {
		if !exp.After(now) {
			delete(l.sessions, k)
		}
	}
	exp := now.Add(SessionTTL)
	l.sessions[id] = exp
	return id, exp
}

func (l *Login) valid(r *http.Request) bool {
	c, err := r.Cookie(SessionCookie)
	if err != nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	exp, ok := l.sessions[c.Value]
	if ok && !exp.After(l.now()) {
		delete(l.sessions, c.Value)
		return false
	}
	return ok
}

// Session returns the user name when r carries a valid sign-in session,
// for a route outside Guard that a signed-in viewer may use (releasing a
// message through the relay checks this itself).
func (l *Login) Session(r *http.Request) (string, bool) {
	if !l.valid(r) {
		return "", false
	}
	return l.name, true
}

func (l *Login) end(r *http.Request) {
	if c, err := r.Cookie(SessionCookie); err == nil {
		l.mu.Lock()
		delete(l.sessions, c.Value)
		l.mu.Unlock()
	}
}

type viewerKey struct{}

// ReadOnly reports whether a request came in through a Login session,
// which may only read.
func ReadOnly(ctx context.Context) bool {
	v, _ := ctx.Value(viewerKey{}).(bool)
	return v
}

// Guard serves the sign-in page and puts next behind a session. Without
// one, a GET is redirected (302) to the sign-in page and anything else
// is refused (401). With one, next serves GET and HEAD only. The page's
// own files under /assets/ are served without a session: the sign-in
// page uses them and they hold no messages.
func (l *Login) Guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == PathLogin:
			l.serveLogin(w, r)
			return
		case r.URL.Path == PathLogout && r.Method == http.MethodPost:
			if !sameOrigin(r) {
				http.Error(w, "Echo Inbox refuses requests from other pages", http.StatusForbidden)
				return
			}
			l.end(r)
			http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, //nolint:gosec // Secure under TLS, as on sign-in
				SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil})
			http.Redirect(w, r, PathLogin, http.StatusSeeOther)
			return
		case strings.HasPrefix(r.URL.Path, "/assets/") && (r.Method == http.MethodGet || r.Method == http.MethodHead):
			next.ServeHTTP(w, r)
			return
		}
		if !l.valid(r) {
			w.Header().Set("Cache-Control", "no-store")
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				http.Error(w, "sign in at "+PathLogin+" first", http.StatusUnauthorized)
				return
			}
			back := r.URL.Path
			if r.URL.RawQuery != "" {
				back += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, PathLogin+"?next="+url.QueryEscape(back), http.StatusFound)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "signed in to view only: changing the inbox needs Echo's token", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), viewerKey{}, true)))
	})
}

// sameOrigin reports whether a browser request came from Echo's own page
// (no Origin header counts: only browsers send it, on cross-site posts).
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	return o == "" || o == "http://"+r.Host || o == "https://"+r.Host
}

// localPath returns p, escaped, when it is a path on this site, else
// "/": a next= that names another host would make the sign-in an open
// redirect. Browsers drop tabs and newlines from a Location and read a
// backslash as a slash, so "/\t/evil.example" and "/\\evil.example" are
// refused along with "//evil.example".
func localPath(p string) string {
	if strings.ContainsFunc(p, func(r rune) bool { return r < 0x20 || r == 0x7f || r == '\\' }) {
		return "/"
	}
	u, err := url.Parse(p)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || u.Opaque != "" ||
		!strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") || u.Path == PathLogin {
		return "/"
	}
	return (&url.URL{Path: u.Path, RawPath: u.RawPath, RawQuery: u.RawQuery}).String()
}

func (l *Login) serveLogin(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Security-Policy", CSP+"; form-action 'self'; frame-ancestors 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		h.Set("Content-Type", "text/html; charset=utf-8")
		_ = loginPage.Execute(w, loginData{Next: localPath(r.URL.Query().Get("next"))})
	case http.MethodPost:
		if !sameOrigin(r) {
			http.Error(w, "Echo Inbox refuses requests from other pages", http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
		next := localPath(r.PostFormValue("next"))
		status, msg := 0, ""
		switch {
		case l.throttled():
			status, msg = http.StatusTooManyRequests, "Too many sign-in attempts. Wait a minute and try again."
		case !l.check(r.PostFormValue("user"), r.PostFormValue("password")):
			l.failed()
			status, msg = http.StatusUnauthorized, "Wrong user name or password."
		}
		if status != 0 {
			h.Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(status)
			_ = loginPage.Execute(w, loginData{Next: next, Error: msg})
			return
		}
		id, exp := l.start()
		// Secure only under TLS: up serves plain HTTP on loopback, where a
		// Secure cookie would never be sent back.
		http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: id, Path: "/", Expires: exp, MaxAge: int(SessionTTL / time.Second), //nolint:gosec // see above
			HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil})
		http.Redirect(w, r, next, http.StatusSeeOther) //nolint:gosec // next went through localPath: a path on this site only
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type loginData struct {
	Next, Error string
}

var loginPage = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sign in: Echo Inbox</title>
<link rel="icon" href="data:,">
<link rel="stylesheet" href="/assets/app.css">
</head>
<body class="signin">
<form class="signin-box" method="post" action="/login">
  <h1>Echo Inbox</h1>
  <p class="signin-note">Sign in to view the messages this site sent. Viewing only: nothing can be changed from here.</p>
  {{if .Error}}<p class="signin-error" role="alert">{{.Error}}</p>{{end}}
  <label>User name <input name="user" autocomplete="username" required autofocus></label>
  <label>Password <input name="password" type="password" autocomplete="current-password" required></label>
  <input type="hidden" name="next" value="{{.Next}}">
  <button type="submit">Sign in</button>
</form>
</body>
</html>
`))
