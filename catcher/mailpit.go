// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package catcher

import (
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/mail"
	"net/textproto"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RhythmShahriar/echo/model"
)

// A read API in Mailpit's shape, so tests and tools written against
// Mailpit (DDEV and Sail helpers, MailHog-era code ported to it) can read
// Echo's email unchanged. Email only; other channels have no Mailpit
// equivalent. The routes take the same token as the rest of the API.
// The subset served:
//
//	GET    /api/v1/messages                    newest first; start, limit
//	GET    /api/v1/search?query=               the same, matching a Mailpit search (see mailpitSearch)
//	GET    /api/v1/message/{ID}                one message (ID "latest" works); marks it read
//	GET    /api/v1/message/{ID}/headers        its headers
//	GET    /api/v1/message/{ID}/raw            its source, masked as stored
//	GET    /api/v1/message/{ID}/part/{PartID}  always 404: Echo keeps no attachment content
//	PUT    /api/v1/messages                    {"IDs": [...], "Read": true}, or "Search", or every email
//	DELETE /api/v1/messages                    {"IDs": [...]}, or every email
//	GET    /api/v1/info                        counts and runtime figures
//	GET    /api/v1/webui                       the web UI settings: no relay, no extras
//
// Not served: tags, release (relay), spam and HTML checks, link checks,
// the chaos and send APIs, thumbnails and DELETE /api/v1/search. Read
// flags live in memory beside the inbox, so a restart marks every message
// unread again.

func (s *Server) mailpitRoutes() {
	s.mux.HandleFunc("GET /api/v1/messages", s.mailpitList)
	s.mux.HandleFunc("GET /api/v1/search", s.mailpitSearch)
	s.mux.HandleFunc("GET /api/v1/message/{id}", s.mailpitMessage)
	s.mux.HandleFunc("GET /api/v1/message/{id}/headers", s.mailpitHeaders)
	s.mux.HandleFunc("GET /api/v1/message/{id}/raw", s.mailpitRaw)
	s.mux.HandleFunc("GET /api/v1/message/{id}/part/{part}", s.mailpitPart)
	s.mux.HandleFunc("PUT /api/v1/messages", s.mailpitSetRead)
	s.mux.HandleFunc("DELETE /api/v1/messages", s.mailpitDelete)
	s.mux.HandleFunc("GET /api/v1/info", s.mailpitInfo)
	s.mux.HandleFunc("GET /api/v1/webui", s.mailpitWebUI)
}

// mailpitState is what the Mailpit routes keep that the inbox doesn't.
type mailpitState struct {
	mu   sync.Mutex
	read map[string]bool // message IDs marked read

	started      time.Time
	smtpAccepted atomic.Int64 // emails the SMTP sink took
	smtpBytes    atomic.Int64 // their raw size
}

// isRead reports whether the email with id was marked read.
func (s *Server) isRead(id string) bool {
	s.pit.mu.Lock()
	defer s.pit.mu.Unlock()
	return s.pit.read[id]
}

// setRead marks the emails ms read or unread, and forgets flags of
// messages the inbox no longer has once they pile up.
func (s *Server) setRead(ms []model.Message, read bool) {
	s.pit.mu.Lock()
	defer s.pit.mu.Unlock()
	for _, m := range ms {
		if read {
			s.pit.read[m.ID] = true
		} else {
			delete(s.pit.read, m.ID)
		}
	}
	if len(s.pit.read) > 1024 {
		for id := range s.pit.read {
			if _, ok := s.Inbox.Get(id); !ok {
				delete(s.pit.read, id)
			}
		}
	}
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
	Username    string           `json:"Username"` // the SMTP login; Echo's sink takes none
	Size        int              `json:"Size"`
	Attachments int              `json:"Attachments"`
	Snippet     string           `json:"Snippet"`
}

type mailpitList struct {
	Total          int              `json:"total"`
	Unread         int              `json:"unread"`
	Count          int              `json:"count"` // older Mailpit versions; newer ones dropped it
	MessagesCount  int              `json:"messages_count"`
	MessagesUnread int              `json:"messages_unread"`
	Start          int              `json:"start"`
	Tags           []string         `json:"tags"`
	Messages       []mailpitSummary `json:"messages"`
}

type mailpitMessage struct {
	ID              string              `json:"ID"`
	MessageID       string              `json:"MessageID"`
	From            *mailpitAddress     `json:"From"`
	To              []mailpitAddress    `json:"To"`
	Cc              []mailpitAddress    `json:"Cc"`
	Bcc             []mailpitAddress    `json:"Bcc"`
	ReplyTo         []mailpitAddress    `json:"ReplyTo"`
	ReturnPath      string              `json:"ReturnPath"`
	Subject         string              `json:"Subject"`
	Date            time.Time           `json:"Date"`
	Tags            []string            `json:"Tags"`
	Username        string              `json:"Username"`
	Text            string              `json:"Text"`
	HTML            string              `json:"HTML"`
	Size            int                 `json:"Size"`
	Inline          []mailpitAttachment `json:"Inline"`
	Attachments     []mailpitAttachment `json:"Attachments"`
	ListUnsubscribe mailpitUnsubscribe  `json:"ListUnsubscribe"`
	Headers         map[string][]string `json:"Headers"` // not in Mailpit's message; it serves them at …/headers
}

// mailpitUnsubscribe is the List-Unsubscribe header as Mailpit reads it.
type mailpitUnsubscribe struct {
	Header     string   `json:"Header"`
	Links      []string `json:"Links"`
	Errors     string   `json:"Errors"`
	HeaderPost string   `json:"HeaderPost"`
}

// unsubscribe reads m's List-Unsubscribe header: its links are the parts
// in angle brackets.
func unsubscribe(m model.Message) mailpitUnsubscribe {
	u := mailpitUnsubscribe{Header: m.Headers["list-unsubscribe"], HeaderPost: m.Headers["list-unsubscribe-post"], Links: []string{}}
	for part := range strings.SplitSeq(u.Header, ",") {
		if l := strings.TrimSpace(part); strings.HasPrefix(l, "<") && strings.HasSuffix(l, ">") {
			u.Links = append(u.Links, strings.Trim(l, "<>"))
		}
	}
	return u
}

// mailpitAttachment is a file as Mailpit lists it. Echo keeps no content,
// so there's no PartID to download it by.
type mailpitAttachment struct {
	FileName    string           `json:"FileName"`
	ContentType string           `json:"ContentType"`
	ContentID   string           `json:"ContentID"`
	Size        int64            `json:"Size"`
	Checksums   mailpitChecksums `json:"Checksums"`
}

type mailpitChecksums struct {
	SHA256 string `json:"SHA256,omitempty"`
}

// files splits m's attachments into Mailpit's inline and attached lists.
func files(m model.Message) (inline, attached []mailpitAttachment) {
	inline, attached = []mailpitAttachment{}, []mailpitAttachment{}
	for _, a := range m.Attachments {
		f := mailpitAttachment{FileName: a.Name, ContentType: a.Type, Size: a.Size, Checksums: mailpitChecksums{SHA256: a.SHA256}}
		if a.Inline {
			inline = append(inline, f)
		} else {
			attached = append(attached, f)
		}
	}
	return inline, attached
}

// emails returns the captured emails, newest first, as Mailpit lists them.
func (s *Server) emails() []model.Message {
	ms := s.Inbox.List(Filter{Channel: model.Email})
	slices.Reverse(ms)
	return ms
}

func (s *Server) mailpitList(w http.ResponseWriter, r *http.Request) {
	ms := s.emails()
	s.mailpitPage(w, r, ms, ms)
}

// mailpitPage answers the page of matched that start and limit ask for,
// counted against every email in all.
func (s *Server) mailpitPage(w http.ResponseWriter, r *http.Request, all, matched []model.Message) {
	q := r.URL.Query()
	start := max(0, atoi(q.Get("start"), 0))
	limit := atoi(q.Get("limit"), 50)
	if limit <= 0 {
		limit = 50
	}
	out := mailpitList{Total: len(all), MessagesCount: len(matched), Start: start, Tags: []string{}, Messages: []mailpitSummary{}}
	s.pit.mu.Lock()
	for _, m := range all {
		if !s.pit.read[m.ID] {
			out.Unread++
		}
	}
	for _, m := range matched {
		if !s.pit.read[m.ID] {
			out.MessagesUnread++
		}
	}
	s.pit.mu.Unlock()
	if start < len(matched) {
		for _, m := range matched[start:min(len(matched), start+limit)] {
			sum := summary(m)
			sum.Read = s.isRead(m.ID)
			out.Messages = append(out.Messages, sum)
		}
	}
	out.Count = len(out.Messages)
	writeJSON(w, http.StatusOK, out)
}

// mailpitSearch answers the emails matching a Mailpit search in query,
// in the list's shape. A search that can't be read is a 400, as Mailpit
// answers.
func (s *Server) mailpitSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if strings.TrimSpace(q.Get("query")) == "" {
		http.Error(w, "query: Mailpit's search needs a query (use /api/v1/messages for every email)", http.StatusBadRequest)
		return
	}
	match, err := s.mailpitQuery(q.Get("query"), q.Get("tz"))
	if err != nil {
		http.Error(w, "query: "+err.Error(), http.StatusBadRequest)
		return
	}
	all := s.emails()
	var matched []model.Message
	for _, m := range all {
		if match(m) {
			matched = append(matched, m)
		}
	}
	s.mailpitPage(w, r, all, matched)
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
	inline, attached := files(m)
	s.setRead([]model.Message{m}, true) // as Mailpit does: reading a message through the API opens it
	writeJSON(w, http.StatusOK, mailpitMessage{ID: sum.ID, MessageID: sum.MessageID, From: sum.From, To: sum.To, Cc: sum.Cc,
		Bcc: sum.Bcc, ReplyTo: sum.ReplyTo, ReturnPath: strings.Trim(m.Headers["return-path"], "<> "), Subject: m.Subject,
		Date: m.At, Tags: []string{}, Text: m.Text, HTML: m.HTML, Size: sum.Size, Inline: inline, Attachments: attached,
		ListUnsubscribe: unsubscribe(m), Headers: headers(m)})
}

// mailpitRaw answers the message's source as Echo stored it: the MIME the
// SMTP sink or a connector sent, masked. A connector that sent only the
// fields gets a source rebuilt from them.
func (s *Server) mailpitRaw(w http.ResponseWriter, r *http.Request) {
	m, ok := s.findEmail(r.PathValue("id"))
	if !ok {
		http.Error(w, "message not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.WriteString(w, rawSource(m)) //nolint:gosec // plain text that browsers are told not to sniff as HTML
}

// rawSource is m's MIME source: the stored body, or one written from the
// headers and text when the connector sent no source.
func rawSource(m model.Message) string {
	if m.Body != "" {
		return m.Body
	}
	h := map[string]string{}
	for k, v := range m.Headers {
		h[textproto.CanonicalMIMEHeaderKey(k)] = v
	}
	set := func(k, v string) {
		if _, ok := h[k]; !ok && v != "" {
			h[k] = v
		}
	}
	set("From", m.From)
	set("To", strings.Join(m.To, ", "))
	set("Cc", strings.Join(m.CC, ", "))
	set("Reply-To", m.ReplyTo)
	set("Subject", m.Subject)
	set("Date", m.At.Format(time.RFC1123Z))
	set("Mime-Version", "1.0")
	body, ctype := m.Text, "text/plain; charset=utf-8"
	if m.Text != "" && m.HTML != "" {
		const boundary = "echo-alternative"
		ctype = `multipart/alternative; boundary="` + boundary + `"`
		body = "--" + boundary + "\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + m.Text + "\r\n--" + boundary +
			"\r\nContent-Type: text/html; charset=utf-8\r\n\r\n" + m.HTML + "\r\n--" + boundary + "--\r\n"
	} else if m.HTML != "" {
		body, ctype = m.HTML, "text/html; charset=utf-8"
	}
	h["Content-Type"] = ctype // the body below is in this form, whatever the connector said
	delete(h, "Content-Transfer-Encoding")
	var b strings.Builder
	for _, k := range slices.Sorted(maps.Keys(h)) {
		b.WriteString(k + ": " + h[k] + "\r\n")
	}
	b.WriteString("\r\n" + body)
	return b.String()
}

// mailpitPart is where Mailpit serves an attachment's content. Echo keeps
// only each file's name, type, size and checksum, so there's nothing to
// serve.
func (s *Server) mailpitPart(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.findEmail(r.PathValue("id")); !ok {
		http.Error(w, "message not found", http.StatusNotFound)
		return
	}
	http.Error(w, "attachment content is not stored: Echo keeps each file's name, type, size and checksum only", http.StatusNotFound)
}

// mailpitSetRead marks emails read or unread: those in IDs, those
// matching Search, or every email when neither is given.
func (s *Server) mailpitSetRead(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs    []string `json:"IDs"`
		Read   bool     `json:"Read"`
		Search string   `json:"Search"`
	}
	if b, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20)); len(strings.TrimSpace(string(b))) > 0 {
		if err := json.Unmarshal(b, &body); err != nil {
			http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	ms := s.emails()
	switch {
	case len(body.IDs) > 0:
		ms = slices.DeleteFunc(ms, func(m model.Message) bool { return !slices.Contains(body.IDs, m.ID) })
	case strings.TrimSpace(body.Search) != "":
		match, err := s.mailpitQuery(body.Search, r.URL.Query().Get("tz"))
		if err != nil {
			http.Error(w, "Search: "+err.Error(), http.StatusBadRequest)
			return
		}
		ms = slices.DeleteFunc(ms, func(m model.Message) bool { return !match(m) })
	}
	s.setRead(ms, body.Read)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "ok")
}

// mailpitInfo is GET /api/v1/info: Mailpit's app information, with
// Echo's figures where Mailpit has a database.
type mailpitInfo struct {
	Version       string         `json:"Version"`
	LatestVersion string         `json:"LatestVersion"`
	Database      string         `json:"Database"`
	DatabaseSize  int64          `json:"DatabaseSize"`
	Messages      int            `json:"Messages"`
	Unread        int            `json:"Unread"`
	Tags          map[string]int `json:"Tags"`
	RuntimeStats  mailpitRuntime `json:"RuntimeStats"`
}

type mailpitRuntime struct {
	Uptime           int64  `json:"Uptime"`
	Memory           uint64 `json:"Memory"`
	MessagesDeleted  int    `json:"MessagesDeleted"`
	SMTPAccepted     int64  `json:"SMTPAccepted"`
	SMTPAcceptedSize int64  `json:"SMTPAcceptedSize"`
	SMTPRejected     int    `json:"SMTPRejected"`
	SMTPIgnored      int    `json:"SMTPIgnored"`
}

func (s *Server) mailpitInfo(w http.ResponseWriter, _ *http.Request) {
	ms := s.emails()
	unread := 0
	for _, m := range ms {
		if !s.isRead(m.ID) {
			unread++
		}
	}
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	s.Inbox.mu.Lock()
	deleted := s.Inbox.deleted
	s.Inbox.mu.Unlock()
	writeJSON(w, http.StatusOK, mailpitInfo{Version: s.cfg.Version, Database: s.cfg.Store, DatabaseSize: s.Inbox.Bytes(),
		Messages: len(ms), Unread: unread, Tags: map[string]int{}, RuntimeStats: mailpitRuntime{
			Uptime: int64(time.Since(s.pit.started).Seconds()), Memory: mem.Alloc, MessagesDeleted: deleted,
			SMTPAccepted: s.pit.smtpAccepted.Load(), SMTPAcceptedSize: s.pit.smtpBytes.Load()}})
}

type mailpitWebUI struct {
	Label               string       `json:"Label"`
	MessageRelay        mailpitRelay `json:"MessageRelay"`
	SpamAssassin        bool         `json:"SpamAssassin"`
	ChaosEnabled        bool         `json:"ChaosEnabled"`
	DuplicatesIgnored   bool         `json:"DuplicatesIgnored"`
	HideDeleteAllButton bool         `json:"HideDeleteAllButton"`
}

// mailpitRelay is off: Echo never delivers what it catches.
type mailpitRelay struct {
	Enabled            bool   `json:"Enabled"`
	SMTPServer         string `json:"SMTPServer"`
	ReturnPath         string `json:"ReturnPath"`
	AllowedRecipients  string `json:"AllowedRecipients"`
	BlockedRecipients  string `json:"BlockedRecipients"`
	OverrideFrom       string `json:"OverrideFrom"`
	PreserveMessageIDs bool   `json:"PreserveMessageIDs"`
}

func (s *Server) mailpitWebUI(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, mailpitWebUI{Label: "Echo"})
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
	_, attached := files(m)
	sum.Attachments = len(attached) // Mailpit counts only the files offered as downloads
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

// mailpitFields are the field names a Mailpit search takes, besides the
// Echo ones (site:, action:, provider:, check:) it passes to ParseQuery.
var mailpitFields = []string{"to", "from", "cc", "bcc", "reply-to", "addressed", "subject", "message-id", "tag", "is", "has",
	"larger", "smaller", "before", "after"}

// echoFields are the Echo search fields a Mailpit search may use too.
var echoFields = []string{"site", "action", "provider", "check"}

// mailpitDates are the forms before: and after: take, as Mailpit reads
// them: year first, or month first when the year comes last.
var mailpitDates = []string{"2006/01/02 15:04:05", "2006/01/02 15:04", "2006/01/02", "2006-01-02 15:04:05", "2006-01-02 15:04",
	"2006-01-02", "01/02/2006 15:04", "01/02/2006", time.RFC3339}

// pitWord is one word of a Mailpit search.
type pitWord struct {
	neg          bool   // prefixed with ! or -
	field, value string // field is "" for free text
}

// splitMailpit splits a Mailpit search at spaces outside double quotes.
// A quote may wrap the whole word or the value after field: alone.
func splitMailpit(s string) ([]pitWord, error) {
	var words []pitWord
	space := func(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }
	for i := 0; i < len(s); {
		if space(s[i]) {
			i++
			continue
		}
		var w pitWord
		if (s[i] == '!' || s[i] == '-') && i+1 < len(s) && !space(s[i+1]) {
			w.neg = true
			i++
		}
		var b strings.Builder
		quoted := false
		for i < len(s) && !space(s[i]) {
			switch c := s[i]; {
			case c == '"':
				j := strings.IndexByte(s[i+1:], '"')
				if j < 0 {
					return nil, errors.New("a quote in the search isn't closed")
				}
				b.WriteString(s[i+1 : i+1+j])
				i += j + 2
				quoted = true
				continue
			case c == ':' && w.field == "" && !quoted:
				if k := strings.ToLower(b.String()); slices.Contains(mailpitFields, k) || slices.Contains(echoFields, k) {
					w.field = k
					b.Reset()
					i++
					continue
				}
			}
			b.WriteByte(s[i])
			i++
		}
		w.value = b.String()
		if w.value == "" && w.field == "" {
			continue // "" on its own
		}
		words = append(words, w)
	}
	return words, nil
}

// mailpitQuery reads a Mailpit search into a test on messages. It takes
// Mailpit's syntax: words that must all match, "quoted phrases", ! or -
// before a word to exclude, and the fields to:, from:, cc:, bcc:,
// reply-to:, addressed:, subject:, message-id:, tag:, is:read, is:unread,
// is:tagged, has:attachment, has:inline, larger:, smaller:, before: and
// after:, plus Echo's site:, action:, provider:, check: and has:issue.
// Free text is Echo's (subject, text, HTML, addresses and source). Echo
// has no tags, so tag: and is:tagged match nothing. As in Mailpit, a size
// or date that doesn't read is ignored. tz names the time zone dates are
// in (default: the machine's).
func (s *Server) mailpitQuery(q, tz string) (func(model.Message) bool, error) {
	words, err := splitMailpit(q)
	if err != nil {
		return nil, err
	}
	loc := time.Local
	if tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	tests := make([]func(model.Message) bool, 0, len(words))
	for _, w := range words {
		test, err := s.mailpitTest(w, loc)
		if err != nil {
			return nil, err
		}
		if w.neg {
			pos := test
			test = func(m model.Message) bool { return !pos(m) }
		}
		tests = append(tests, test)
	}
	return func(m model.Message) bool {
		for _, t := range tests {
			if !t(m) {
				return false
			}
		}
		return true
	}, nil
}

// mailpitTest is the test for one word of a Mailpit search.
func (s *Server) mailpitTest(w pitWord, loc *time.Location) (func(model.Message) bool, error) {
	v := strings.ToLower(w.value)
	contains := func(fields func(model.Message) []string) func(model.Message) bool {
		return func(m model.Message) bool {
			return slices.ContainsFunc(fields(m), func(f string) bool { return strings.Contains(strings.ToLower(f), v) })
		}
	}
	addrs := func(list []string, header string) []string { return append(slices.Clone(list), header) }
	always := func(model.Message) bool { return true }
	if w.value == "" {
		return nil, errors.New(w.field + ": needs a value")
	}
	switch w.field {
	case "to":
		return contains(func(m model.Message) []string { return addrs(m.To, m.Headers["to"]) }), nil
	case "from":
		return contains(func(m model.Message) []string { return []string{m.From, m.Headers["from"]} }), nil
	case "cc":
		return contains(func(m model.Message) []string { return addrs(m.CC, m.Headers["cc"]) }), nil
	case "bcc":
		return contains(func(m model.Message) []string { return m.BCC }), nil
	case "reply-to":
		return contains(func(m model.Message) []string { return []string{m.ReplyTo, m.Headers["reply-to"]} }), nil
	case "addressed":
		return contains(func(m model.Message) []string {
			out := append(addrs(m.To, m.Headers["to"]), m.From, m.Headers["from"], m.ReplyTo, m.Headers["reply-to"], m.Headers["cc"])
			return append(append(out, m.CC...), m.BCC...)
		}), nil
	case "subject":
		return contains(func(m model.Message) []string { return []string{m.Subject} }), nil
	case "message-id":
		return contains(func(m model.Message) []string { return []string{strings.Trim(m.Headers["message-id"], "<> ")} }), nil
	case "tag":
		return func(model.Message) bool { return false }, nil
	case "is":
		switch v {
		case "read":
			return func(m model.Message) bool { return s.isRead(m.ID) }, nil
		case "unread":
			return func(m model.Message) bool { return !s.isRead(m.ID) }, nil
		case "tagged":
			return func(model.Message) bool { return false }, nil
		}
		return nil, errors.New("is:" + w.value + ": is takes read, unread or tagged")
	case "has":
		switch strings.TrimSuffix(v, "s") {
		case "attachment":
			return func(m model.Message) bool { _, attached := files(m); return len(attached) > 0 }, nil
		case "inline":
			return func(m model.Message) bool { inline, _ := files(m); return len(inline) > 0 }, nil
		case "issue":
			return func(m model.Message) bool { return len(m.Issues) > 0 }, nil
		}
		return nil, errors.New("has:" + w.value + ": has takes attachment, inline or issue")
	case "larger", "smaller":
		n, ok := mailpitSize(v)
		if !ok {
			return always, nil
		}
		if w.field == "larger" {
			return func(m model.Message) bool { return int64(summary(m).Size) > n }, nil
		}
		return func(m model.Message) bool { return int64(summary(m).Size) < n }, nil
	case "before", "after":
		t, ok := mailpitDate(w.value, loc)
		if !ok {
			return always, nil
		}
		if w.field == "before" {
			return func(m model.Message) bool { return m.At.Before(t) }, nil
		}
		return func(m model.Message) bool { return m.At.After(t) }, nil
	}
	t, err := newTerm(w.field, w.value) // free text and Echo's fields
	if err != nil {
		return nil, err
	}
	return t.match, nil
}

// mailpitSize reads a size such as 2M, 1.5MB, 500K or 2048 (bytes).
func mailpitSize(v string) (int64, bool) {
	mult := 1.0
	for _, u := range []struct {
		suffix string
		mult   float64
	}{{"kb", 1 << 10}, {"mb", 1 << 20}, {"k", 1 << 10}, {"m", 1 << 20}, {"b", 1}} {
		if n, ok := strings.CutSuffix(v, u.suffix); ok {
			v, mult = n, u.mult
			break
		}
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || f < 0 {
		return 0, false
	}
	return int64(f * mult), true
}

// mailpitDate reads a date in one of mailpitDates, in loc unless it names
// its own zone.
func mailpitDate(v string, loc *time.Location) (time.Time, bool) {
	for _, layout := range mailpitDates {
		if t, err := time.ParseInLocation(layout, v, loc); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
