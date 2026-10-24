package recognize

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Small helpers shared by the provider files.

func jsonFake(status int, body string) Fake {
	return Fake{Status: status, Headers: map[string]string{"content-type": "application/json"}, Body: body}
}

func xmlFake(body string) Fake {
	return Fake{Status: 200, Headers: map[string]string{"content-type": "text/xml"}, Body: body}
}

func textFake(status int, body string) Fake {
	return Fake{Status: status, Headers: map[string]string{"content-type": "text/plain; charset=utf-8"}, Body: body}
}

// js marshals v for a fake body, without HTML escaping (Mailgun IDs are
// "<…@domain>"). Fakes only marshal plain maps and strings, which can't
// fail.
func js(v any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return strings.TrimSuffix(b.String(), "\n")
}

// randHex returns n random hex characters for fake IDs. The message ID
// isn't known yet when a fake is made (the inbox assigns it afterwards),
// so fakes can't derive their IDs from it.
func randHex(n int) string {
	b := make([]byte, (n+1)/2)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:n]
}

// uuid returns a random version-4-shaped UUID.
func uuid() string {
	h := randHex(32)
	return h[0:8] + "-" + h[8:12] + "-4" + h[13:16] + "-a" + h[17:20] + "-" + h[20:32]
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

// values reads form fields from a URL-encoded or multipart body (Mailgun's
// SDK posts multipart). File parts are recorded by name only, under
// "attachment".
func values(r Request) url.Values {
	mt, params, err := mime.ParseMediaType(r.Header("content-type"))
	if err != nil || mt != "multipart/form-data" || params["boundary"] == "" {
		return r.Form()
	}
	v := url.Values{}
	mr := multipart.NewReader(bytes.NewReader(r.Body), params["boundary"])
	for {
		p, err := mr.NextPart()
		if err != nil {
			return v
		}
		if p.FileName() != "" {
			v.Add("attachment", p.FileName())
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(p, 1<<20))
		v.Add(p.FormName(), string(b))
	}
}

// isJSON reports whether the body looks like JSON (content type or shape).
func isJSON(r Request) bool {
	if strings.Contains(r.Header("content-type"), "json") {
		return true
	}
	b := bytes.TrimSpace(r.Body)
	return len(b) > 0 && (b[0] == '{' || b[0] == '[')
}

// split turns address lists ("a@x, b@y") into trimmed entries.
func split(lists ...string) []string {
	var out []string
	for _, l := range lists {
		for _, a := range strings.Split(l, ",") {
			if a = strings.TrimSpace(a); a != "" {
				out = append(out, a)
			}
		}
	}
	return out
}

// strs flattens a decoded JSON value (a string, a number, a list of them,
// or objects with one of keys) into strings.
func strs(v any, keys ...string) []string {
	switch t := v.(type) {
	case string:
		return split(t)
	case float64:
		return []string{strconv.FormatFloat(t, 'f', -1, 64)}
	case []any:
		var out []string
		for _, e := range t {
			out = append(out, strs(e, keys...)...)
		}
		return out
	case map[string]any:
		for _, k := range keys {
			if s := strs(t[k], keys...); len(s) > 0 {
				return s
			}
		}
	}
	return nil
}

// str returns v as a string when it is one.
func str(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	}
	return ""
}

// obj returns v as a JSON object, or an empty one.
func obj(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

// dig walks nested JSON objects by key.
func dig(v any, keys ...string) any {
	for _, k := range keys {
		v = obj(v)[k]
	}
	return v
}

// segments returns the non-empty path segments of r.
func segments(r Request) []string {
	var out []string
	for _, s := range strings.Split(r.URL.Path, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// verb names what a REST call does from its method and whether it targets
// one object.
func verb(method string, withID bool) string {
	switch method {
	case "GET":
		if withID {
			return "retrieve"
		}
		return "list"
	case "DELETE":
		return "delete"
	case "PUT", "PATCH":
		return "update"
	}
	if withID {
		return "update"
	}
	return "create"
}

// numeric matches IDs made of digits (ActiveCampaign, HubSpot, Meta).
var numeric = regexp.MustCompile(`^\d+$`)

// graphPath matches Meta Graph API paths: /v19.0/{id}/{edge}.
func graphPath(r Request, edge string) bool {
	s := segments(r)
	if len(s) > 0 && strings.HasPrefix(s[0], "v") {
		s = s[1:]
	}
	return len(s) == 2 && numeric.MatchString(s[0]) && s[1] == edge
}

// graphID returns the node ID in a Meta Graph API path.
func graphID(r Request) string {
	s := segments(r)
	if len(s) > 0 && strings.HasPrefix(s[0], "v") {
		s = s[1:]
	}
	if len(s) == 0 {
		return ""
	}
	return s[0]
}
