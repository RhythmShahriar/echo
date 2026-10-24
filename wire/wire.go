// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package wire is the small JSON protocol between Echo and the connectors
// inside a site (the WordPress mu-plugin, the Laravel package, the Node
// package): a connector reports each email and each outbound HTTP
// request, and for HTTP requests Echo answers whether to let it through or
// what to answer instead. Keep it stable: connectors in other languages
// implement it.
//
//	POST /v1/capture   Capture → Decision
package wire

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

// Version is sent by connectors and checked by Echo.
const Version = 1

// RunHeader carries "<run>/<action>" from the thing that triggered a
// request (Soundcheck's browser, `rhythm echo run`) to the connector,
// which copies it into every capture, so messages are tied to the action
// that caused them. Connectors also accept it as a cookie of the same name.
const RunHeader = "X-Echo-Run"

// RunEnv carries the same "<run>/<action>" value as RunHeader to work
// done outside a request. Echo sets it on the commands it runs (WP-CLI,
// artisan, scripts). The connectors and sendmail read it.
const RunEnv = "RHYTHM_ECHO_RUN"

// SiteEnv is the site's own address (a URL or a host), set by whoever
// configures the connector. Connectors use it, never a request header, to
// tell calls to the site itself from calls to providers.
const SiteEnv = "RHYTHM_ECHO_SITE"

// TokenHeader authenticates connectors when Echo listens beyond loopback.
const TokenHeader = "X-Echo-Token" //nolint:gosec // a header name, not a credential

// Phases of an HTTP capture.
const (
	Before = "before" // about to send: Echo decides
	After  = "after"  // sent (observe mode, or an allowed host): the response
)

// Capture is one email or HTTP request a connector saw.
type Capture struct {
	Version int    `json:"v"`
	Kind    string `json:"kind"`            // email or http
	Phase   string `json:"phase,omitempty"` // http: before or after; email: after (with id) reports a real send's outcome
	ID      string `json:"id,omitempty"`    // after: the ID Echo returned for before
	Run     string `json:"run,omitempty"`   // from X-Echo-Run, "<run>/<action>"
	Source  Source `json:"source"`
	Email   *Email `json:"email,omitempty"`
	HTTP    *HTTP  `json:"http,omitempty"`
	Error   string `json:"error,omitempty"` // the send failed (wp_mail_failed, a transport error)
}

// Source describes the connector and what it hooked.
type Source struct {
	Via    string `json:"via"`              // wordpress, laravel, node, smtp, proxy
	Stack  string `json:"stack,omitempty"`  // wordpress, laravel, node, php
	Plugin string `json:"plugin,omitempty"` // best guess at the sender: a plugin slug, a package
	Hook   string `json:"hook,omitempty"`   // wp_mail, pre_http_request, fetch, nodemailer…
	Host   string `json:"host,omitempty"`   // the site's own host (always allowed)
	Site   string `json:"site,omitempty"`   // the site's ID when one Echo serves many sites
}

// Email is a message handed to the site's mailer.
type Email struct {
	From    string            `json:"from,omitempty"`
	To      []string          `json:"to,omitempty"`
	CC      []string          `json:"cc,omitempty"`
	BCC     []string          `json:"bcc,omitempty"`
	ReplyTo string            `json:"reply_to,omitempty"`
	Subject string            `json:"subject,omitempty"`
	Text    string            `json:"text,omitempty"`
	HTML    string            `json:"html,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Raw     string            `json:"raw,omitempty"` // full MIME, when the connector has it

	// Attachments lists the files attached to the message, for connectors
	// that send no Raw MIME. With Raw set, Echo reads them from the MIME.
	Attachments []Attachment `json:"attachments,omitempty"`
}

// Attachment describes one attached file. Connectors send what they know
// about it, never its content.
type Attachment struct {
	Name   string `json:"name,omitempty"`
	Type   string `json:"type,omitempty"`   // MIME type, application/pdf
	Size   int64  `json:"size,omitempty"`   // decoded size in bytes
	SHA256 string `json:"sha256,omitempty"` // hex digest of the decoded content
}

// HTTP is an outbound request and, after sending, its response.
type HTTP struct {
	Method   string            `json:"method"`
	URL      string            `json:"url"`
	Headers  map[string]string `json:"headers,omitempty"`
	Body     string            `json:"body,omitempty"`
	Status   int               `json:"status,omitempty"` // after
	Response string            `json:"response,omitempty"`
}

// Decisions for an HTTP capture.
const (
	Allow = "allow" // send it for real
	Fake  = "fake"  // don't send; answer with Response
	Block = "block" // don't send; fail the request
)

// Decision is Echo's answer to a capture.
type Decision struct {
	ID       string    `json:"id"`
	Mode     string    `json:"mode"`               // seal or observe
	Action   string    `json:"action,omitempty"`   // http: allow, fake or block; email: fake (don't send) or allow
	Response *Response `json:"response,omitempty"` // for fake
}

// Response is the answer a connector gives the site instead of sending.
type Response struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

// sigLen is the length of a run signature: 16 bytes in unpadded base64url.
const sigLen = 22

// SignRun returns "<run>/<action>.<sig>", where sig is the first 16 bytes
// of HMAC-SHA256(token, "<run>/<action>") in unpadded base64url. A
// connector that knows the token accepts a run value from a request only
// when it verifies, so a visitor cannot add messages to a baseline or drop
// them from one. With no token there is nothing to sign with and the value
// is returned unsigned.
func SignRun(token, run, action string) string {
	v := run + "/" + action
	if token == "" {
		return v
	}
	return v + "." + runSig(token, v)
}

// VerifyRun splits a run value into its run and action and reports whether
// it carries a valid signature for token. An unsigned "<run>/<action>"
// still returns its run and action, with ok false, so callers that accept
// unsigned values (no token configured) can use them. A value with a
// signature that does not verify returns nothing.
func VerifyRun(token, value string) (run, action string, ok bool) {
	v, sig := value, ""
	if i := strings.LastIndexByte(value, '.'); i >= 0 && isSig(value[i+1:]) {
		v, sig = value[:i], value[i+1:]
	}
	run, action, found := strings.Cut(v, "/")
	if !found || run == "" {
		return "", "", false
	}
	if sig == "" {
		return run, action, false
	}
	if token == "" || !hmac.Equal([]byte(sig), []byte(runSig(token, v))) {
		return "", "", false
	}
	return run, action, true
}

func runSig(token, v string) string {
	m := hmac.New(sha256.New, []byte(token))
	m.Write([]byte(v))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:16])
}

// isSig reports whether s has the shape of a signature, so an action name
// with a dot in it ("order.paid") is not mistaken for a signed value.
func isSig(s string) bool {
	if len(s) != sigLen {
		return false
	}
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return false
		}
	}
	return true
}
