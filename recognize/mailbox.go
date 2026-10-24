// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package recognize

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"strings"

	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/rhythm/kit/secrets"
)

// Mailbox APIs: the site sends as a signed-in mailbox (OAuth) instead of
// through a mail service. WP Mail SMTP's Outlook and Google mailers and
// most "send with Microsoft 365 / Gmail" plugins call these.
func init() {
	register(graphMail)
	register(gmail)
}

// graphAddr is Microsoft Graph's recipient: {"emailAddress": {"address"}}.
type graphAddr struct {
	EmailAddress struct {
		Address string `json:"address"`
	} `json:"emailAddress"`
}

func graphAddrs(as []graphAddr) []string {
	out := make([]string, 0, len(as))
	for _, a := range as {
		if a.EmailAddress.Address != "" {
			out = append(out, a.EmailAddress.Address)
		}
	}
	return out
}

// graphSendMail reports whether r is POST /{version}/users/{id}/sendMail
// or /{version}/me/sendMail.
func graphSendMail(r Request) bool {
	s := segments(r)
	switch {
	case r.Method != "POST" || len(s) < 3 || !strings.EqualFold(s[len(s)-1], "sendMail"):
		return false
	case len(s) == 3:
		return s[1] == "me"
	case len(s) == 4:
		return s[1] == "users"
	}
	return false
}

// graphMail: Microsoft Graph sendMail. The body is JSON with a message
// object, or (Content-Type text/plain) a base64 MIME message. It answers
// 202 with no body.
var graphMail = Recognizer{
	Provider: "microsoft-graph",
	Name:     "Microsoft Graph",
	Channel:  model.Email,
	Hosts:    []string{"graph.microsoft.com", "graph.microsoft.us", "dod-graph.microsoft.us", "microsoftgraph.chinacloudapi.cn"},
	Match: func(r Request) bool {
		return host(r, "graph.microsoft.com", "graph.microsoft.us", "dod-graph.microsoft.us", "microsoftgraph.chinacloudapi.cn") && graphSendMail(r)
	},
	Fill: func(r Request, m *model.Message) {
		if !isJSON(r) {
			if raw, ok := decodeB64(string(r.Body)); ok {
				fillMIME(raw, m)
				maskB64(m, string(r.Body), raw, base64.StdEncoding)
			}
		} else {
			var p struct {
				Message struct {
					Subject string `json:"subject"`
					Body    struct {
						ContentType string `json:"contentType"`
						Content     string `json:"content"`
					} `json:"body"`
					From    graphAddr   `json:"from"`
					Sender  graphAddr   `json:"sender"`
					To      []graphAddr `json:"toRecipients"`
					CC      []graphAddr `json:"ccRecipients"`
					BCC     []graphAddr `json:"bccRecipients"`
					ReplyTo []graphAddr `json:"replyTo"`
				} `json:"message"`
			}
			if r.JSON(&p) {
				msg := p.Message
				m.Subject = msg.Subject
				m.To, m.CC, m.BCC = graphAddrs(msg.To), graphAddrs(msg.CC), graphAddrs(msg.BCC)
				m.ReplyTo = first(graphAddrs(msg.ReplyTo))
				m.From = first(graphAddrs([]graphAddr{msg.From, msg.Sender}))
				if strings.EqualFold(msg.Body.ContentType, "html") {
					m.HTML = msg.Body.Content
				} else {
					m.Text = msg.Body.Content
				}
			}
		}
		// Without a from, Graph sends as the mailbox in the path.
		if s := segments(r); m.From == "" && len(s) == 4 && strings.Contains(s[2], "@") {
			m.From = s[2]
		}
	},
	Fake: func(Request, *model.Message) Fake { return Fake{Status: 202} },
}

// gmailSend reports whether r is Gmail's users.messages.send, as a JSON
// call or a media upload: [/upload]/gmail/v1/users/{id}/messages/send.
func gmailSend(r Request) bool {
	s := segments(r)
	if len(s) > 0 && s[0] == "upload" {
		s = s[1:]
	}
	return r.Method == "POST" && len(s) == 6 && s[0] == "gmail" && s[1] == "v1" && s[2] == "users" &&
		s[4] == "messages" && s[5] == "send"
}

// gmail: the Gmail API's users.messages.send. The message is a whole
// RFC 5322 message: base64url in {"raw"} for a JSON call, the body itself
// (message/rfc822) or one part of a multipart/related body for an upload.
var gmail = Recognizer{
	Provider: "gmail",
	Name:     "Gmail API",
	Channel:  model.Email,
	Hosts:    []string{"gmail.googleapis.com", "www.googleapis.com"},
	Match:    func(r Request) bool { return host(r, "gmail.googleapis.com", "www.googleapis.com") && gmailSend(r) },
	Fill: func(r Request, m *model.Message) {
		if raw, encoded := gmailRaw(r); raw != nil {
			fillMIME(raw, m)
			if encoded != "" {
				maskB64(m, encoded, raw, base64.RawURLEncoding)
			}
		}
	},
	Fake: func(Request, *model.Message) Fake {
		id := "18" + randHex(14)
		return jsonFake(200, js(map[string]any{"id": id, "threadId": id, "labelIds": []string{"SENT"}}))
	},
}

// gmailRaw returns the RFC 5322 message a Gmail send carries, or nil,
// and the base64 it was decoded from ("" when it came as is).
func gmailRaw(r Request) ([]byte, string) {
	mt, params, _ := mime.ParseMediaType(r.Header("content-type"))
	switch {
	case mt == "message/rfc822":
		return r.Body, ""
	case mt == "multipart/related" && params["boundary"] != "":
		mr := multipart.NewReader(bytes.NewReader(r.Body), params["boundary"])
		for {
			p, err := mr.NextPart()
			if err != nil {
				return nil, ""
			}
			b, _ := io.ReadAll(io.LimitReader(p, maxMIME))
			pt, _, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
			if pt == "message/rfc822" {
				return b, ""
			}
		}
	}
	var p struct {
		Raw string `json:"raw"`
	}
	if !r.JSON(&p) {
		return nil, ""
	}
	raw, _ := decodeB64(p.Raw)
	return raw, p.Raw
}

// maskB64 masks secrets in a message that came base64-encoded. The stored
// body is the request as sent, where secrets.Redact can't see into the
// base64: when the decoded message holds a secret, the encoded run in
// m.Body is swapped for the masked message, encoded again with enc. When
// the run isn't there whole (the body was cut to size), m.Body becomes
// the masked message itself, no longer than it was.
func maskB64(m *model.Message, encoded string, raw []byte, enc *base64.Encoding) {
	masked := secrets.Redact(string(raw))
	if masked == string(raw) {
		return
	}
	if strings.Contains(m.Body, encoded) {
		m.Body = strings.Replace(m.Body, encoded, enc.EncodeToString([]byte(masked)), 1)
		return
	}
	if m.Body != "" && len(masked) > len(m.Body) {
		masked = masked[:len(m.Body)]
	}
	m.Body = masked
}

// decodeB64 decodes base64 in any of its spellings: URL-safe or standard,
// padded or not, wrapped over lines. Gmail asks for base64url but its
// client libraries differ in padding.
func decodeB64(s string) ([]byte, bool) {
	s = strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, s)
	s = strings.TrimRight(s, "=")
	if s == "" {
		return nil, false
	}
	for _, enc := range []*base64.Encoding{base64.RawURLEncoding, base64.RawStdEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, true
		}
	}
	return nil, false
}
