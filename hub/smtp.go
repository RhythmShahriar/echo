// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package hub

import (
	"bytes"
	"crypto/tls"
	"io"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"

	"github.com/RhythmShahriar/echo/catcher"
)

// SMTP returns the hub's SMTP sink on addr. A site logs in with any
// username and its write token as the password, which says whose mail it
// is; nothing is accepted before that. With tlsCfg the sink offers
// STARTTLS and takes no login before it, so tokens never cross in clear.
func (h *Hub) SMTP(addr string, tlsCfg *tls.Config) *smtp.Server {
	srv := catcher.NewSMTP(h.srv, addr)
	srv.Backend = siteBackend{inner: srv.Backend, hub: h}
	if tlsCfg != nil {
		srv.TLSConfig = tlsCfg
		srv.AllowInsecureAuth = false
	}
	return srv
}

// maxMail is the most the hub reads of one message; the catcher's sink
// refuses more than its own 25 MiB limit.
const maxMail = 25 << 20

// siteBackend wraps the catcher's sink so every session must log in with
// a site's write token before it may send.
type siteBackend struct {
	inner smtp.Backend
	hub   *Hub
}

// NewSession wraps the sink's session in a siteSession.
func (b siteBackend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	s, err := b.inner.NewSession(c)
	if err != nil {
		return nil, err
	}
	return &siteSession{Session: s, hub: b.hub}, nil
}

var errNeedAuth = &smtp.SMTPError{Code: 530, EnhancedCode: smtp.EnhancedCode{5, 7, 0},
	Message: "Authentication required: log in with any username and the site's write token as the password"}

// siteSession is a sink session that takes no mail before AUTH and files
// what it takes under the site that logged in.
type siteSession struct {
	smtp.Session
	hub  *Hub
	site string
}

// AuthMechanisms offers PLAIN only: the password is the write token.
func (s *siteSession) AuthMechanisms() []string { return []string{sasl.Plain} }

// Auth accepts a site's write token only: the hub's sink takes mail from
// sites, each under its own token, so the read token can't send.
func (s *siteSession) Auth(string) (sasl.Server, error) {
	return sasl.NewPlainServer(func(_, _, password string) error {
		site := s.hub.tokenSite(password)
		if site == "" || site == "read" {
			return smtp.ErrAuthFailed
		}
		s.site = site
		if ss, ok := s.Session.(catcher.SiteSetter); ok {
			ss.SetSite(site)
		}
		return nil
	}), nil
}

// Mail refuses a sender before AUTH.
func (s *siteSession) Mail(from string, o *smtp.MailOptions) error {
	if s.site == "" {
		return errNeedAuth
	}
	return s.Session.Mail(from, o)
}

// Rcpt refuses a recipient before AUTH.
func (s *siteSession) Rcpt(to string, o *smtp.RcptOptions) error {
	if s.site == "" {
		return errNeedAuth
	}
	return s.Session.Rcpt(to, o)
}

// Data refuses a message before AUTH, then captures it as the site's.
func (s *siteSession) Data(r io.Reader) error {
	if s.site == "" {
		return errNeedAuth
	}
	// Read the message before taking the capture lock, so a slow client
	// holds up no other site. The sink checks the size again.
	raw, err := io.ReadAll(io.LimitReader(r, maxMail+1))
	if err != nil {
		return err
	}
	return s.hub.as(s.site, func() error { return s.Session.Data(bytes.NewReader(raw)) })
}
