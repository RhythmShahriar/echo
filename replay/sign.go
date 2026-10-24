// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package replay

import (
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // some receivers still verify HMAC-SHA1; HMAC-SHA1 isn't broken
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// HMAC configures the generic scheme.
type HMAC struct {
	Header    string `json:"header,omitempty"`    // default X-Signature
	Algorithm string `json:"algorithm,omitempty"` // sha256 (default), sha1, sha512
	Encoding  string `json:"encoding,omitempty"`  // hex (default), base64
	Prefix    string `json:"prefix,omitempty"`    // put before the digest, e.g. "sha256="
	// Timestamp, when set, is a header carrying the Unix time; the signed
	// content is then "<timestamp>.<body>".
	Timestamp string `json:"timestamp,omitempty"`
}

// ErrPayPal is returned for PayPal events.
var ErrPayPal = errors.New("PayPal webhooks can't be replayed: receivers verify them by asking PayPal's API " +
	"(POST /v1/notifications/verify-webhook-signature) or with PayPal's signing certificate, so no local secret can sign one. " +
	"Use the PayPal sandbox instead: Developer Dashboard → Webhooks simulator, pointed at the staging URL, with sandbox credentials")

// secretEnv is where each scheme's secret is read from by default.
var secretEnv = map[string]string{
	Stripe:      "ECHO_STRIPE_WEBHOOK_SECRET",
	Standard:    "ECHO_WEBHOOK_SECRET",
	GitHub:      "ECHO_GITHUB_WEBHOOK_SECRET",
	WooCommerce: "ECHO_WC_WEBHOOK_SECRET",
	HMACScheme:  "ECHO_WEBHOOK_SECRET",
}

// SecretEnv returns the environment variable a scheme's secret is read
// from when Options.SecretEnv is empty.
func SecretEnv(scheme string) string { return secretEnv[scheme] }

// Sign returns the headers that sign e's body with secret, as the provider
// would send them. It doesn't check the secret's shape; Send does.
func Sign(e Event, secret string, cfg HMAC, now time.Time) (http.Header, error) {
	h := http.Header{}
	ts := strconv.FormatInt(now.Unix(), 10)
	switch e.Scheme {
	case Stripe:
		// https://docs.stripe.com/webhooks#verify-manually: HMAC-SHA256 keyed
		// with the whole whsec_ string over "<t>.<payload>".
		sig := hexMAC(sha256.New, []byte(secret), ts+"."+string(e.Body))
		h.Set("Stripe-Signature", "t="+ts+",v1="+sig)
		h.Set("User-Agent", "Stripe/1.0 (+https://stripe.com/docs/webhooks)")
	case Standard:
		// https://www.standardwebhooks.com: the key is the base64 part of
		// whsec_…, the content "<id>.<timestamp>.<payload>", the signature
		// "v1,<base64>". Svix-based senders (Resend, Clerk) send the same
		// values as svix-* headers, which their libraries check first.
		key, err := standardKey(secret)
		if err != nil {
			return nil, err
		}
		id := "msg_" + randomID(27)
		mac := hmac.New(sha256.New, key)
		mac.Write([]byte(id + "." + ts + "." + string(e.Body)))
		sig := "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
		for _, p := range []string{"webhook", "svix"} {
			h.Set(p+"-id", id)
			h.Set(p+"-timestamp", ts)
			h.Set(p+"-signature", sig)
		}
		h.Set("User-Agent", "Svix-Webhooks/1.0 (rhythm-echo replay)")
	case GitHub:
		// https://docs.github.com/webhooks/using-webhooks/validating-webhook-deliveries
		h.Set("X-Hub-Signature-256", "sha256="+hexMAC(sha256.New, []byte(secret), string(e.Body)))
		h.Set("X-GitHub-Event", orDefault(e.Type, "ping"))
		h.Set("X-GitHub-Delivery", uuid())
		h.Set("X-GitHub-Hook-ID", "123456789")
		h.Set("User-Agent", "GitHub-Hookshot/echo")
	case WooCommerce:
		// WC_Webhook::generate_signature: base64(hash_hmac('sha256', body, secret, true)).
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(e.Body)
		resource, event, _ := strings.Cut(orDefault(e.Type, "order.created"), ".")
		h.Set("X-WC-Webhook-Signature", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
		h.Set("X-WC-Webhook-Source", "https://woocommerce.echo.test/")
		h.Set("X-WC-Webhook-Topic", resource+"."+event)
		h.Set("X-WC-Webhook-Resource", resource)
		h.Set("X-WC-Webhook-Event", event)
		h.Set("X-WC-Webhook-ID", "1")
		h.Set("X-WC-Webhook-Delivery-ID", randomDigits(6))
		h.Set("User-Agent", "WooCommerce/9.3.3 Hookshot (WordPress/6.6.2)")
	case HMACScheme:
		alg, err := algorithm(cfg.Algorithm)
		if err != nil {
			return nil, err
		}
		content := string(e.Body)
		if cfg.Timestamp != "" {
			content = ts + "." + content
			h.Set(cfg.Timestamp, ts)
		}
		mac := hmac.New(alg, []byte(secret))
		mac.Write([]byte(content))
		var digest string
		switch strings.ToLower(orDefault(cfg.Encoding, "hex")) {
		case "hex":
			digest = hex.EncodeToString(mac.Sum(nil))
		case "base64":
			digest = base64.StdEncoding.EncodeToString(mac.Sum(nil))
		default:
			return nil, fmt.Errorf("unknown HMAC encoding %q (hex or base64)", cfg.Encoding)
		}
		h.Set(orDefault(cfg.Header, "X-Signature"), cfg.Prefix+digest)
		h.Set("User-Agent", "rhythm-echo replay")
	case PayPal:
		return nil, ErrPayPal
	default:
		return nil, fmt.Errorf("unknown signing scheme %q", e.Scheme)
	}
	h.Set("Content-Type", "application/json")
	return h, nil
}

// signatureHeaders are masked in results.
func signatureHeaders(cfg HMAC) []string {
	return []string{"Stripe-Signature", "Webhook-Signature", "Svix-Signature", "X-Hub-Signature-256", "X-Wc-Webhook-Signature",
		http.CanonicalHeaderKey(orDefault(cfg.Header, "X-Signature"))}
}

func standardKey(secret string) ([]byte, error) {
	b64, ok := strings.CutPrefix(secret, "whsec_")
	if !ok {
		return nil, errors.New("a Standard Webhooks secret starts with whsec_")
	}
	key, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(key) == 0 {
		return nil, errors.New("a Standard Webhooks secret is whsec_ followed by base64")
	}
	return key, nil
}

func algorithm(name string) (func() hash.Hash, error) {
	switch strings.ToLower(orDefault(name, "sha256")) {
	case "sha256":
		return sha256.New, nil
	case "sha1":
		return sha1.New, nil
	case "sha512":
		return sha512.New, nil
	}
	return nil, fmt.Errorf("unknown HMAC algorithm %q (sha256, sha1 or sha512)", name)
}

func hexMAC(alg func() hash.Hash, key []byte, content string) string {
	mac := hmac.New(alg, key)
	mac.Write([]byte(content))
	return hex.EncodeToString(mac.Sum(nil))
}

// uuid returns a random version 4 UUID, as GitHub's X-GitHub-Delivery.
func uuid() string {
	b := randomBytes(16)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	x := hex.EncodeToString(b)
	return x[0:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:32]
}

func randomDigits(n int) string {
	out := randomBytes(n)
	for i := range out {
		out[i] = '0' + out[i]%10
	}
	if out[0] == '0' {
		out[0] = '1'
	}
	return string(out)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
