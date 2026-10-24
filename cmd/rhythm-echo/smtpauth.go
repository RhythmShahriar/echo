package main

import (
	"crypto/subtle"
	"io"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
)

// tokenBackend makes the SMTP sink require AUTH PLAIN with Echo's token as
// the password (any username). It wraps the sink when it listens beyond
// loopback, which, like the API, is only allowed with a token: otherwise
// anyone who can reach the port could add mail to the captures.
type tokenBackend struct {
	inner smtp.Backend
	token string
}

func (b tokenBackend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	s, err := b.inner.NewSession(c)
	if err != nil {
		return nil, err
	}
	return &tokenSession{Session: s, token: b.token}, nil
}

var errNeedAuth = &smtp.SMTPError{Code: 530, EnhancedCode: smtp.EnhancedCode{5, 7, 0},
	Message: "Authentication required: log in with any username and Echo's token as the password"}

type tokenSession struct {
	smtp.Session
	token  string
	authed bool
}

func (s *tokenSession) AuthMechanisms() []string { return []string{sasl.Plain} }

func (s *tokenSession) Auth(string) (sasl.Server, error) {
	return sasl.NewPlainServer(func(_, _, password string) error {
		if subtle.ConstantTimeCompare([]byte(password), []byte(s.token)) != 1 {
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
