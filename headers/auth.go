package headers

import (
	"mime"
	"net/mail"
	"net/textproto"
	"strconv"
	"strings"
)

type mimeDecoder = mime.WordDecoder

// parseAuth picks the receiver's verdict: the topmost Authentication-Results
// (added last, by the final receiver; lower ones may be forged by the
// sender), or, when there is none, the ARC-Authentication-Results with the
// highest instance.
func parseAuth(h mail.Header) Auth {
	if ar := h[textproto.CanonicalMIMEHeaderKey("Authentication-Results")]; len(ar) > 0 {
		return parseAR(ar[0], false)
	}
	best, bestI := "", -1
	for _, v := range h[textproto.CanonicalMIMEHeaderKey("ARC-Authentication-Results")] {
		first, _, _ := strings.Cut(v, ";")
		k, n, _ := strings.Cut(strings.TrimSpace(first), "=")
		if strings.EqualFold(strings.TrimSpace(k), "i") {
			if i, err := strconv.Atoi(strings.TrimSpace(n)); err == nil && i > bestI {
				best, bestI = v, i
			}
		}
	}
	if bestI < 0 {
		return Auth{}
	}
	_, rest, _ := strings.Cut(best, ";")
	return parseAR(rest, true)
}

// parseAR reads an RFC 8601 header: "authserv-id [version]; method=result
// reason=... ptype.prop=value; ...". Comments are dropped.
func parseAR(v string, arc bool) Auth {
	a := Auth{ARC: arc}
	parts := splitOutside(stripComments(v), ';')
	if len(parts) == 0 {
		return a
	}
	if f := strings.Fields(parts[0]); len(f) > 0 {
		a.Server = strings.ToLower(f[0])
	}
	for _, p := range parts[1:] {
		f := fields(p)
		if len(f) == 0 {
			continue
		}
		name, res, ok := strings.Cut(f[0], "=")
		if !ok {
			continue
		}
		name, _, _ = strings.Cut(strings.ToLower(name), "/")
		m := Method{Method: name, Result: strings.ToLower(res)}
		for _, kv := range f[1:] {
			k, val, ok := strings.Cut(kv, "=")
			if !ok {
				continue
			}
			val = strings.Trim(val, `"`)
			if strings.EqualFold(k, "reason") {
				m.Reason = val
				continue
			}
			if m.Props == nil {
				m.Props = map[string]string{}
			}
			m.Props[strings.ToLower(k)] = val
		}
		a.Methods = append(a.Methods, m)
		switch name {
		case "spf":
			if a.SPF == "" {
				a.SPF = m.Result
			}
		case "dkim":
			if a.DKIM == "" || m.Result == "pass" {
				a.DKIM = m.Result
			}
		case "dmarc":
			if a.DMARC == "" {
				a.DMARC = m.Result
			}
		}
	}
	return a
}

// prop returns a property of the first result for method.
func (a Auth) prop(method, key string) string {
	for _, m := range a.Methods {
		if m.Method == method {
			return m.Props[key]
		}
	}
	return ""
}

// stripComments drops RFC 5322 comments (nested parentheses) outside
// quoted strings.
func stripComments(s string) string {
	var b strings.Builder
	depth, quoted, esc := 0, false, false
	for _, r := range s {
		switch {
		case esc:
			esc = false
		case r == '\\':
			esc = true
		case r == '"' && depth == 0:
			quoted = !quoted
		case r == '(' && !quoted:
			depth++
			continue
		case r == ')' && !quoted && depth > 0:
			depth--
			continue
		}
		if depth == 0 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func splitOutside(s string, sep rune) []string {
	var out []string
	var b strings.Builder
	quoted := false
	for _, r := range s {
		if r == '"' {
			quoted = !quoted
		}
		if r == sep && !quoted {
			out = append(out, strings.TrimSpace(b.String()))
			b.Reset()
			continue
		}
		b.WriteRune(r)
	}
	if t := strings.TrimSpace(b.String()); t != "" {
		out = append(out, t)
	}
	return out
}

// fields splits on whitespace outside quoted strings.
func fields(s string) []string {
	var out []string
	var b strings.Builder
	quoted := false
	flush := func() {
		if b.Len() > 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	for _, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
			b.WriteRune(r)
		case (r == ' ' || r == '\t') && !quoted:
			flush()
		default:
			b.WriteRune(r)
		}
	}
	flush()
	return out
}
