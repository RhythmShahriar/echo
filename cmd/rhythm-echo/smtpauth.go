// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"crypto/subtle"
	"io"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
)

// tokenBackend makes the SMTP sink require AUTH PLAIN with one of Echo's
// tokens as the password (any username): its token, or with --sites the
// read token or a site's write token. It wraps the sink when it listens
// beyond loopback, which, like the API, is only allowed with a token:
// otherwise anyone who can reach the port could add mail to the captures.
type tokenBackend struct {
	inner  smtp.Backend
	tokens []string
}

func (b tokenBackend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	s, err := b.inner.NewSession(c)
	if err != nil {
		return nil, err
	}
	return &tokenSession{Session: s, tokens: b.tokens}, nil
}

var errNeedAuth = &smtp.SMTPError{Code: 530, EnhancedCode: smtp.EnhancedCode{5, 7, 0},
	Message: "Authentication required: log in with any username and Echo's token as the password"}

type tokenSession struct {
	smtp.Session
	tokens []string
	authed bool
}

func (s *tokenSession) AuthMechanisms() []string { return []string{sasl.Plain} }

func (s *tokenSession) Auth(string) (sasl.Server, error) {
	return sasl.NewPlainServer(func(_, _, password string) error {
		ok := false
		for _, t := range s.tokens { // every one, so the time doesn't tell which nearly matched
			if t != "" && subtle.ConstantTimeCompare([]byte(password), []byte(t)) == 1 {
				ok = true
			}
		}
		if !ok {
			return smtp.ErrAuthFailed
		}
		s.authed = true
		return nil
	}), nil
}

func (s *tokenSession) Mail(from string, o *smtp.MailOptions) error {
	if !s.authed {
		return errNeedAuth
	}
	return s.Session.Mail(from, o)
}

func (s *tokenSession) Rcpt(to string, o *smtp.RcptOptions) error {
	if !s.authed {
		return errNeedAuth
	}
	return s.Session.Rcpt(to, o)
}

func (s *tokenSession) Data(r io.Reader) error {
	if !s.authed {
		return errNeedAuth
	}
	return s.Session.Data(r)
}
