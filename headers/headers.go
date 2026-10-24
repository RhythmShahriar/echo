// Package headers analyzes a received email's headers: what the receiving
// server concluded about SPF, DKIM and DMARC, how the message is signed,
// which service sent it and the path it took. The inferred sender feeds
// dns.Check, which then checks the domain's records for that sender.
package headers

import (
	"bytes"
	"net"
	"net/mail"
	"net/textproto"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/RhythmShahriar/rhythm/kit/secrets"

	"github.com/RhythmShahriar/echo/dns"
	"github.com/RhythmShahriar/echo/model"
)

// Result is what Analyze found.
type Result struct {
	From       string        `json:"from,omitempty"`
	FromDomain string        `json:"from_domain,omitempty"`
	Subject    string        `json:"subject,omitempty"`
	MessageID  string        `json:"message_id,omitempty"`
	ReturnPath string        `json:"return_path,omitempty"`
	Sender     dns.Sender    `json:"sender"`
	Provider   string        `json:"provider,omitempty"` // label of Sender.Provider
	Auth       Auth          `json:"auth"`
	Signatures []Signature   `json:"signatures,omitempty"`
	Hops       []Hop         `json:"hops,omitempty"` // in the order the message traveled
	Issues     []model.Issue `json:"issues"`
}

// Auth is the receiving server's verdict, from its Authentication-Results
// header (or, when there is none, the newest ARC-Authentication-Results).
type Auth struct {
	Server  string   `json:"server,omitempty"` // authserv-id: mx.google.com
	ARC     bool     `json:"arc,omitempty"`    // taken from ARC-Authentication-Results
	SPF     string   `json:"spf,omitempty"`    // pass, fail, softfail, neutral, none, temperror, permerror
	DKIM    string   `json:"dkim,omitempty"`   // pass when any signature passed
	DMARC   string   `json:"dmarc,omitempty"`
	Methods []Method `json:"methods,omitempty"`
}

// Method is one result in an Authentication-Results header.
type Method struct {
	Method string            `json:"method"`
	Result string            `json:"result"`
	Reason string            `json:"reason,omitempty"`
	Props  map[string]string `json:"props,omitempty"` // header.from, smtp.mailfrom, header.d, header.s…
}

// Signature is a DKIM-Signature header's tags (b= and bh= omitted).
type Signature struct {
	Domain    string   `json:"d"`
	Selector  string   `json:"s"`
	Algorithm string   `json:"a"`
	Headers   []string `json:"h"`
	Length    bool     `json:"l,omitempty"` // signs only part of the body
}

// Hop is one Received header.
type Hop struct {
	From  string    `json:"from,omitempty"`
	IP    string    `json:"ip,omitempty"`
	By    string    `json:"by,omitempty"`
	With  string    `json:"with,omitempty"`
	At    time.Time `json:"at,omitzero"`
	Delay float64   `json:"delay_s,omitempty"` // since the previous hop
	Raw   string    `json:"raw"`
}

// Analyze parses a raw RFC 5322 message (an .eml file, as saved from the
// recipient's mailbox) and reports on its authentication.
func Analyze(raw []byte) Result {
	r := Result{}
	msg, err := mail.ReadMessage(bytes.NewReader(normalizeEOL(raw)))
	if err != nil {
		r.issue("parse", model.Error, "Message cannot be parsed", err.Error(),
			"Save the original message (in Gmail: Show original, then Download original) and run the check on that file.")
		r.Issues = []model.Issue{r.Issues[0]}
		return r
	}
	h := msg.Header
	r.Subject = decode(h.Get("Subject"))
	r.MessageID = strings.TrimSpace(h.Get("Message-Id"))
	if a, err := mail.ParseAddress(h.Get("From")); err == nil {
		r.From = a.Address
		r.FromDomain = domainOf(a.Address)
	}
	if a := strings.Trim(strings.TrimSpace(h.Get("Return-Path")), "<>"); a != "" {
		r.ReturnPath = a
	}
	r.Hops = parseHops(h[textproto.CanonicalMIMEHeaderKey("Received")])
	r.Auth = parseAuth(h)
	for _, v := range h[textproto.CanonicalMIMEHeaderKey("DKIM-Signature")] {
		r.Signatures = append(r.Signatures, parseSignature(v))
	}

	r.Sender = dns.Sender{ReturnPathDomain: domainOf(r.ReturnPath)}
	if r.Sender.ReturnPathDomain == "" {
		r.Sender.ReturnPathDomain = domainOf(r.Auth.prop("spf", "smtp.mailfrom"))
	}
	if sig := r.bestSignature(); sig != nil {
		r.Sender.DKIMDomain, r.Sender.DKIMSelector = sig.Domain, sig.Selector
	}
	r.Sender.SMTPHost = relayIP(h, r.Hops)
	r.Sender.Provider = infer(h, &r)
	if p, ok := dns.ProviderByName(r.Sender.Provider); ok {
		r.Provider = p.Label
	}

	r.checkAuth()
	r.checkSignatures()
	r.checkAlignment()
	r.checkHeaders(h)
	sort.SliceStable(r.Issues, func(i, j int) bool { return r.Issues[i].Level.Rank() < r.Issues[j].Level.Rank() })
	if r.Issues == nil {
		r.Issues = []model.Issue{}
	}
	return r
}

func (r *Result) issue(check string, level model.Level, title, detail, fix string) {
	i := model.Issue{Check: "headers/" + check, Level: level, Title: title, Detail: detail, Fix: fix}
	i.Identify()
	r.Issues = append(r.Issues, i)
}

// bestSignature prefers a signature aligned with the From domain (the one
// DMARC can pass on), then the first.
func (r *Result) bestSignature() *Signature {
	for i := range r.Signatures {
		if dns.Aligned(r.Signatures[i].Domain, r.FromDomain, false) {
			return &r.Signatures[i]
		}
	}
	if len(r.Signatures) > 0 {
		return &r.Signatures[0]
	}
	return nil
}

// normalizeEOL lets net/mail read files saved with bare LF line endings
// or a leading mbox "From " line.
func normalizeEOL(raw []byte) []byte {
	if bytes.HasPrefix(raw, []byte("From ")) {
		if i := bytes.IndexByte(raw, '\n'); i >= 0 {
			raw = raw[i+1:]
		}
	}
	return raw
}

func decode(s string) string {
	if d, err := new(mimeDecoder).DecodeHeader(s); err == nil {
		return d
	}
	return s
}

func domainOf(addr string) string {
	addr = strings.Trim(strings.TrimSpace(addr), "<>")
	if i := strings.LastIndexByte(addr, '@'); i >= 0 {
		addr = addr[i+1:]
	} else if !strings.Contains(addr, ".") {
		return ""
	}
	return strings.TrimSuffix(strings.ToLower(addr), ".")
}

var (
	reFrom = regexp.MustCompile(`(?i)^\s*from\s+(\S+)(?:\s+\(([^)]*)\))?`)
	reBy   = regexp.MustCompile(`(?i)\bby\s+([^\s;()]+)`)
	reWith = regexp.MustCompile(`(?i)\bwith\s+([^\s;()]+)`)
	reIP   = regexp.MustCompile(`\[(?:IPv6:)?([0-9A-Fa-f:.]+)\]`)
)

// parseHops reads Received headers (newest first, as they appear) and
// returns them oldest first, with the delay between hops.
func parseHops(vals []string) []Hop {
	hops := make([]Hop, 0, len(vals))
	for i := len(vals) - 1; i >= 0; i-- {
		v := strings.Join(strings.Fields(vals[i]), " ")
		hop := Hop{Raw: secrets.Redact(v)}
		clause := v
		if j := strings.LastIndexByte(v, ';'); j >= 0 {
			clause = v[:j]
			if t, err := mail.ParseDate(strings.TrimSpace(v[j+1:])); err == nil {
				hop.At = t
			}
		}
		byAt := len(clause)
		if m := reBy.FindStringSubmatchIndex(clause); m != nil {
			hop.By = strings.ToLower(clause[m[2]:m[3]])
			byAt = m[0]
		}
		if m := reFrom.FindStringSubmatch(clause[:byAt]); m != nil {
			hop.From = strings.ToLower(m[1])
		}
		if m := reIP.FindStringSubmatch(clause[:byAt]); m != nil {
			hop.IP = m[1]
		}
		if m := reWith.FindStringSubmatch(clause); m != nil {
			hop.With = m[1]
		}
		if n := len(hops); n > 0 && !hop.At.IsZero() && !hops[n-1].At.IsZero() {
			hop.Delay = hop.At.Sub(hops[n-1].At).Seconds()
		}
		hops = append(hops, hop)
	}
	return hops
}

var reClientIP = regexp.MustCompile(`(?i)client-ip=([0-9A-Fa-f:.]+)`)

// relayIP is the public address that handed the message to the receiver:
// Received-SPF's client-ip, else the newest hop with a public from-address.
func relayIP(h mail.Header, hops []Hop) string {
	if m := reClientIP.FindStringSubmatch(h.Get("Received-Spf")); m != nil {
		return m[1]
	}
	for i := len(hops) - 1; i >= 0; i-- {
		if ip := net.ParseIP(hops[i].IP); ip != nil && public(ip) {
			return ip.String()
		}
	}
	return ""
}

func public(ip net.IP) bool {
	return !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() && !ip.IsUnspecified()
}

// parseSignature reads DKIM-Signature tags.
func parseSignature(v string) Signature {
	s := Signature{}
	for _, part := range strings.Split(v, ";") {
		k, val, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		val = strings.Join(strings.Fields(val), "")
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "d":
			s.Domain = strings.ToLower(val)
		case "s":
			s.Selector = val
		case "a":
			s.Algorithm = strings.ToLower(val)
		case "h":
			for _, f := range strings.Split(val, ":") {
				if f = strings.ToLower(strings.TrimSpace(f)); f != "" {
					s.Headers = append(s.Headers, f)
				}
			}
		case "l":
			s.Length = true
		}
	}
	return s
}
