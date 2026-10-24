package catcher

import (
	"encoding/json"
	"io"
	"net/http"
	"net/mail"
	"net/textproto"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/model"
)

// A read API in Mailpit's shape, so tests and tools written against
// Mailpit (or MailHog-era helpers ported to it) can read Echo's email
// unchanged: list, read and delete. Email only; other channels have no
// Mailpit equivalent. The routes take the same token as the rest of the
// API.
//
//	GET    /api/v1/messages              newest first; start, limit
//	GET    /api/v1/message/{ID}          one message (ID "latest" works)
//	GET    /api/v1/message/{ID}/headers  its headers
//	DELETE /api/v1/messages              {"IDs": [...]}, or every email

func (s *Server) mailpitRoutes() {
	s.mux.HandleFunc("GET /api/v1/messages", s.mailpitList)
	s.mux.HandleFunc("GET /api/v1/message/{id}", s.mailpitMessage)
	s.mux.HandleFunc("GET /api/v1/message/{id}/headers", s.mailpitHeaders)
	s.mux.HandleFunc("DELETE /api/v1/messages", s.mailpitDelete)
}

type mailpitAddress struct {
	Name    string `json:"Name"`
	Address string `json:"Address"`
}

type mailpitSummary struct {
	ID          string           `json:"ID"`
	MessageID   string           `json:"MessageID"`
	Read        bool             `json:"Read"`
	From        *mailpitAddress  `json:"From"`
	To          []mailpitAddress `json:"To"`
	Cc          []mailpitAddress `json:"Cc"`
	Bcc         []mailpitAddress `json:"Bcc"`
	ReplyTo     []mailpitAddress `json:"ReplyTo"`
	Subject     string           `json:"Subject"`
	Created     time.Time        `json:"Created"`
	Tags        []string         `json:"Tags"`
	Size        int              `json:"Size"`
	Attachments int              `json:"Attachments"`
	Snippet     string           `json:"Snippet"`
}

type mailpitList struct {
	Total         int              `json:"total"`
	Unread        int              `json:"unread"`
	Count         int              `json:"count"`
	MessagesCount int              `json:"messages_count"`
	Start         int              `json:"start"`
	Tags          []string         `json:"tags"`
	Messages      []mailpitSummary `json:"messages"`
}

type mailpitMessage struct {
	ID          string              `json:"ID"`
	MessageID   string              `json:"MessageID"`
	From        *mailpitAddress     `json:"From"`
	To          []mailpitAddress    `json:"To"`
	Cc          []mailpitAddress    `json:"Cc"`
	Bcc         []mailpitAddress    `json:"Bcc"`
	ReplyTo     []mailpitAddress    `json:"ReplyTo"`
	ReturnPath  string              `json:"ReturnPath"`
	Subject     string              `json:"Subject"`
	Date        time.Time           `json:"Date"`
	Tags        []string            `json:"Tags"`
	Text        string              `json:"Text"`
	HTML        string              `json:"HTML"`
	Size        int                 `json:"Size"`
	Inline      []any               `json:"Inline"`
	Attachments []any               `json:"Attachments"`
	Headers     map[string][]string `json:"Headers"` // not in Mailpit's message; it serves them at …/headers
}

// emails returns the captured emails, newest first, as Mailpit lists them.
func (s *Server) emails() []model.Message {
	ms := s.Inbox.List(Filter{Channel: model.Email})
	slices.Reverse(ms)
	return ms
}

func (s *Server) mailpitList(w http.ResponseWriter, r *http.Request) {
	ms := s.emails()
	q := r.URL.Query()
	start := max(0, atoi(q.Get("start"), 0))
	limit := atoi(q.Get("limit"), 50)
	if limit <= 0 {
		limit = 50
	}
	out := mailpitList{Total: len(ms), MessagesCount: len(ms), Start: start, Tags: []string{}, Messages: []mailpitSummary{}}
	if start < len(ms) {
		for _, m := range ms[start:min(len(ms), start+limit)] {
			out.Messages = append(out.Messages, summary(m))
		}
	}
	out.Count = len(out.Messages)
	writeJSON(w, http.StatusOK, out)
}

// findEmail finds a captured email by ID, or the newest for "latest".
func (s *Server) findEmail(id string) (model.Message, bool) {
	if id == "latest" {
		if ms := s.emails(); len(ms) > 0 {
			return ms[0], true
		}
		return model.Message{}, false
	}
	m, ok := s.Inbox.Get(id)
	return m, ok && m.Channel == model.Email
}

func (s *Server) mailpitMessage(w http.ResponseWriter, r *http.Request) {
	m, ok := s.findEmail(r.PathValue("id"))
	if !ok {
		http.Error(w, "message not found", http.StatusNotFound)
		return
	}
	sum := summary(m)
	writeJSON(w, http.StatusOK, mailpitMessage{ID: sum.ID, MessageID: sum.MessageID, From: sum.From, To: sum.To, Cc: sum.Cc,
		Bcc: sum.Bcc, ReplyTo: sum.ReplyTo, ReturnPath: strings.Trim(m.Headers["return-path"], "<> "), Subject: m.Subject,
		Date: m.At, Tags: []string{}, Text: m.Text, HTML: m.HTML, Size: sum.Size, Inline: []any{}, Attachments: []any{},
		Headers: headers(m)})
}

func (s *Server) mailpitHeaders(w http.ResponseWriter, r *http.Request) {
	m, ok := s.findEmail(r.PathValue("id"))
	if !ok {
		http.Error(w, "message not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, headers(m))
}

// mailpitDelete deletes the emails named in {"IDs": [...]}, or every
// email when none are named. Mailpit answers "ok".
func (s *Server) mailpitDelete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []string `json:"IDs"`
	}
	if b, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20)); len(strings.TrimSpace(string(b))) > 0 {
		if err := json.Unmarshal(b, &body); err != nil {
			http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	if len(body.IDs) == 0 {
		s.Inbox.Clear(Filter{Channel: model.Email})
	} else {
		s.Inbox.Delete(model.Email, body.IDs)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "ok")
}

func summary(m model.Message) mailpitSummary {
	sum := mailpitSummary{ID: m.ID, MessageID: strings.Trim(m.Headers["message-id"], "<> "), Read: false,
		To: addresses(m.To, m.Headers["to"]), Cc: addresses(m.CC, m.Headers["cc"]), Bcc: addresses(m.BCC, ""), Subject: m.Subject, Created: m.At,
		Tags: []string{}, Size: len(m.Body), Snippet: snippet(m)}
	if sum.Size == 0 {
		sum.Size = len(m.Text) + len(m.HTML)
	}
	if m.ReplyTo != "" {
		sum.ReplyTo = addresses([]string{m.ReplyTo}, m.Headers["reply-to"])
	} else {
		sum.ReplyTo = []mailpitAddress{}
	}
	if m.From != "" {
		a := addresses([]string{m.From}, m.Headers["from"])[0]
		sum.From = &a
	}
	return sum
}

// addresses turns recipients into Mailpit's form. Echo keeps addresses;
// the display names come from the header they were read from, when the
// message has it.
func addresses(list []string, header string) []mailpitAddress {
	names := map[string]string{}
	if parsed, err := mail.ParseAddressList(header); err == nil {
		for _, a := range parsed {
			names[strings.ToLower(a.Address)] = a.Name
		}
	}
	out := make([]mailpitAddress, 0, len(list))
	for _, s := range list {
		a := mailpitAddress{Address: strings.TrimSpace(s)}
		if p, err := mail.ParseAddress(s); err == nil {
			a = mailpitAddress{Name: p.Name, Address: p.Address}
		}
		if a.Name == "" {
			a.Name = names[strings.ToLower(a.Address)]
		}
		out = append(out, a)
	}
	return out
}

// headers returns m's headers with their usual capitalization. Echo keeps
// one value per name (repeated headers joined), Mailpit a list.
func headers(m model.Message) map[string][]string {
	out := make(map[string][]string, len(m.Headers))
	for k, v := range m.Headers {
		out[textproto.CanonicalMIMEHeaderKey(k)] = []string{v}
	}
	return out
}

var (
	snippetTags  = regexp.MustCompile(`(?s)<style.*?</style>|<script.*?</script>|<[^>]+>`)
	snippetSpace = regexp.MustCompile(`\s+`)
)

// snippet is the start of the readable text, as Mailpit shows it (250
// characters).
func snippet(m model.Message) string {
	t := m.Text
	if strings.TrimSpace(t) == "" {
		t = snippetTags.ReplaceAllString(m.HTML, " ")
	}
	t = strings.TrimSpace(snippetSpace.ReplaceAllString(t, " "))
	if r := []rune(t); len(r) > 250 {
		t = string(r[:250]) + "..."
	}
	return t
}

func atoi(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
