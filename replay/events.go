package replay

import (
	"bytes"
	"crypto/rand"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Signing schemes.
const (
	Stripe      = "stripe"      // Stripe-Signature
	Standard    = "standard"    // Standard Webhooks / Svix: Resend, Clerk…
	GitHub      = "github"      // X-Hub-Signature-256
	WooCommerce = "woocommerce" // X-WC-Webhook-Signature
	HMACScheme  = "hmac"        // generic, configured by Options.HMAC
	PayPal      = "paypal"      // can't be forged: Send refuses
)

// Schemes lists the signing schemes Send knows.
var Schemes = []string{Stripe, Standard, GitHub, WooCommerce, HMACScheme, PayPal}

// The library's directories map to schemes.
var dirScheme = map[string]string{
	"stripe": Stripe, "resend": Standard, "clerk": Standard, "github": GitHub, "woocommerce": WooCommerce, "paypal": PayPal,
}

//go:embed events
var library embed.FS

// Event is a webhook to replay.
type Event struct {
	Name   string `json:"name"`   // "stripe/payment_intent.succeeded", or the file path
	Scheme string `json:"scheme"` // how it's signed
	Type   string `json:"type"`   // event type or topic: payment_intent.succeeded, order.created, ping
	Body   []byte `json:"-"`
}

// Events lists the library, sorted: "stripe/charge.refunded", ….
func Events() []string {
	var out []string
	_ = fs.WalkDir(library, "events", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".json") {
			out = append(out, strings.TrimSuffix(strings.TrimPrefix(p, "events/"), ".json"))
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// Load returns a library event by name. The provider prefix may be left
// out when the type is unique ("invoice.paid"). Stripe events get a fresh
// evt_test_ ID and created time, so receivers that de-duplicate on the
// event ID process every replay.
func Load(name string) (Event, error) {
	full := ""
	for _, n := range Events() {
		if n == name || (!strings.Contains(name, "/") && path.Base(n) == name) {
			if full != "" && full != n {
				return Event{}, fmt.Errorf("event %q is ambiguous: %s or %s", name, full, n)
			}
			full = n
		}
	}
	if full == "" {
		return Event{}, fmt.Errorf("unknown event %q (try one of: %s)", name, strings.Join(Events(), ", "))
	}
	b, err := library.ReadFile("events/" + full + ".json")
	if err != nil {
		return Event{}, err
	}
	dir, typ := path.Split(full)
	e := Event{Name: full, Scheme: dirScheme[strings.TrimSuffix(dir, "/")], Type: typ, Body: b}
	if e.Scheme == Stripe {
		if err := e.Set("id", strconv.Quote("evt_test_"+randomID(24))); err != nil {
			return Event{}, err
		}
		if err := e.Set("created", strconv.FormatInt(time.Now().Unix(), 10)); err != nil {
			return Event{}, err
		}
	}
	return e, nil
}

// FromFile reads a JSON payload to sign with scheme. typ is the event type
// or topic; when empty it's taken from the payload's "type" field.
func FromFile(file, scheme, typ string) (Event, error) {
	b, err := os.ReadFile(file) //nolint:gosec // the user names the payload file
	if err != nil {
		return Event{}, err
	}
	if !json.Valid(b) {
		return Event{}, fmt.Errorf("%s is not valid JSON", file)
	}
	if scheme == "" {
		scheme = HMACScheme
	}
	if !known(scheme) {
		return Event{}, fmt.Errorf("unknown signing scheme %q (one of: %s)", scheme, strings.Join(Schemes, ", "))
	}
	if typ == "" {
		var p struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(b, &p)
		typ = p.Type
	}
	return Event{Name: file, Scheme: scheme, Type: typ, Body: b}, nil
}

func known(scheme string) bool {
	for _, s := range Schemes {
		if s == scheme {
			return true
		}
	}
	return false
}

// Set overrides one field of the payload. Paths are dot-separated keys,
// with numbers indexing arrays: "data.object.customer_email",
// "data.object.metadata.order_id", "line_items.0.quantity". Missing
// objects are created. value is JSON when it parses as JSON (2000, true,
// null, "quoted", {"a":1}); anything else is a string, so
// customer_email=jamie@example.net needs no quotes but order_id="123"
// keeps a numeric-looking value a string.
func (e *Event) Set(fieldPath, value string) error {
	var root any
	d := json.NewDecoder(bytes.NewReader(e.Body))
	d.UseNumber()
	if err := d.Decode(&root); err != nil {
		return fmt.Errorf("payload is not JSON: %w", err)
	}
	var v any
	vd := json.NewDecoder(strings.NewReader(value))
	vd.UseNumber()
	if err := vd.Decode(&v); err != nil || vd.More() {
		v = value
	}
	keys := strings.Split(fieldPath, ".")
	for _, k := range keys {
		if k == "" {
			return fmt.Errorf("bad path %q", fieldPath)
		}
	}
	root, err := set(root, keys, v, fieldPath)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(root); err != nil {
		return err
	}
	e.Body = buf.Bytes()
	return nil
}

// SetAll applies "path=value" overrides in order.
func (e *Event) SetAll(sets []string) error {
	for _, s := range sets {
		k, v, ok := strings.Cut(s, "=")
		if !ok {
			return fmt.Errorf("override %q: want path=value", s)
		}
		if err := e.Set(strings.TrimSpace(k), v); err != nil {
			return fmt.Errorf("override %q: %w", s, err)
		}
	}
	return nil
}

func set(node any, keys []string, v any, full string) (any, error) {
	if len(keys) == 0 {
		return v, nil
	}
	k := keys[0]
	switch n := node.(type) {
	case map[string]any:
		child, err := set(n[k], keys[1:], v, full)
		if err != nil {
			return nil, err
		}
		n[k] = child
		return n, nil
	case []any:
		i, err := strconv.Atoi(k)
		if err != nil || i < 0 || i > len(n) {
			return nil, fmt.Errorf("%s: %q is not an index of a %d-item array", full, k, len(n))
		}
		if i == len(n) {
			n = append(n, nil)
		}
		child, err := set(n[i], keys[1:], v, full)
		if err != nil {
			return nil, err
		}
		n[i] = child
		return n, nil
	case nil:
		child, err := set(nil, keys[1:], v, full)
		if err != nil {
			return nil, err
		}
		return map[string]any{k: child}, nil
	default:
		return nil, fmt.Errorf("%s: %q is inside a %T, not an object", full, k, node)
	}
}

const idChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

func randomID(n int) string {
	b := make([]byte, n)
	lim := big.NewInt(int64(len(idChars)))
	for i := range b {
		x, err := rand.Int(rand.Reader, lim)
		if err != nil {
			panic(err) // crypto/rand doesn't fail on supported platforms
		}
		b[i] = idChars[x.Int64()]
	}
	return string(b)
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand doesn't fail on supported platforms
	}
	return b
}
