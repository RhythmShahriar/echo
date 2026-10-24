// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package dns

import (
	"encoding/asn1"
	"encoding/base64"
	"fmt"
	"math/big"
	"strings"

	"github.com/RhythmShahriar/echo/model"
)

// DKIMKey is a published DKIM public key.
type DKIMKey struct {
	Selector string `json:"selector"`
	Domain   string `json:"domain"`
	Name     string `json:"name"`            // selector._domainkey.domain
	CNAME    string `json:"cname,omitempty"` // where the name points, for provider-hosted keys
	KeyType  string `json:"key_type"`        // rsa, ed25519
	Bits     int    `json:"bits,omitempty"`
	Revoked  bool   `json:"revoked,omitempty"`
	Problem  string `json:"problem,omitempty"`
}

var (
	oidRSA     = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}
	oidEd25519 = asn1.ObjectIdentifier{1, 3, 101, 112}
)

func (c *checker) dkim() {
	d := c.rep.Domain
	s := c.o.Sender
	// Which selectors, at which domain, and how sure we are they should exist.
	type probe struct {
		sel, domain string
	}
	var probes []probe
	certain, provider := false, Provider{}
	if s != nil {
		provider, _ = ProviderByName(s.Provider)
	}
	switch {
	case s != nil && s.DKIMSelector != "" && (s.DKIMDomain == "" || OrgDomain(s.DKIMDomain) == c.rep.OrgDomain):
		dom := d
		if s.DKIMDomain != "" {
			dom = normalize(s.DKIMDomain)
		}
		probes = append(probes, probe{normalize(s.DKIMSelector), dom})
		certain = true
	case provider.Name != "" && len(provider.Selectors) > 0:
		for _, sel := range provider.Selectors {
			probes = append(probes, probe{sel, d})
		}
	default:
		for _, sel := range commonSelectors() {
			probes = append(probes, probe{sel, d})
		}
	}

	for _, p := range probes {
		k, found, err := c.dkimKey(p.sel, p.domain)
		if err != nil {
			c.issue("dkim", model.Warning, "DKIM lookup failed", fmt.Sprintf("TXT %s._domainkey.%s: %v", p.sel, p.domain, err),
				"Check the domain's nameservers answer TXT queries, then run the check again.")
			return
		}
		if found {
			c.rep.DKIM = append(c.rep.DKIM, k)
		}
	}

	if len(c.rep.DKIM) == 0 {
		switch {
		case certain:
			p := probes[0]
			c.issue("dkim", model.Error, "DKIM key not found for the sending selector",
				fmt.Sprintf("Mail is signed with selector %q (d=%s), but %s._domainkey.%s has no DKIM record, so DKIM fails at every receiver.", p.sel, p.domain, p.sel, p.domain),
				fmt.Sprintf("Publish the TXT (or CNAME) record your sending service gives for %s._domainkey.%s.", p.sel, p.domain))
		case provider.Name != "" && len(provider.Selectors) > 0:
			names := make([]string, 0, len(probes))
			for _, p := range probes {
				names = append(names, p.sel+"._domainkey."+p.domain)
			}
			c.issue("dkim", model.Warning, "No DKIM key found for the sending service",
				fmt.Sprintf("None of %s's default selectors has a key: %s. Without a DKIM signature from your own domain, DMARC can only pass on SPF, which breaks when mail is forwarded.", provider.Label, strings.Join(names, ", ")),
				"Set up domain authentication in "+provider.Label+" and publish the DKIM records it gives you under "+d+".")
		case provider.Note != "":
			c.issue("dkim", model.Notice, "DKIM selector unknown for the sending service", provider.Note,
				"Run `rhythm echo headers` on a message the site sent, then check again with its selector.")
		default:
			c.issue("dkim", model.Notice, "No DKIM key found at common selectors",
				"No key was found at the usual selectors ("+strings.Join(commonSelectors(), ", ")+"). The domain may use another selector; the check can't list them.",
				"Run `rhythm echo headers` on a message the site sent to learn its selector, or publish a DKIM key (TXT at <selector>._domainkey."+d+") from your sending service.")
		}
		return
	}

	for _, k := range c.rep.DKIM {
		switch {
		case k.Revoked:
			c.issue("dkim", model.Error, "DKIM key is revoked",
				k.Name+" has an empty p=, which revokes the key: signatures with selector "+k.Selector+" fail.",
				"Publish the current key at "+k.Name+", or switch the sending service to an active selector.")
		case k.Problem != "":
			c.issue("dkim", model.Error, "DKIM record is invalid", k.Name+": "+k.Problem,
				"Republish the TXT record at "+k.Name+` exactly as your sending service gives it: "v=DKIM1; k=rsa; p=<base64 key>".`)
		case k.KeyType == "rsa" && k.Bits < 1024:
			c.issue("dkim", model.Error, "DKIM key is shorter than 1024 bits",
				fmt.Sprintf("%s holds a %d-bit RSA key. Receivers ignore keys under 1024 bits (RFC 8301), so DKIM fails.", k.Name, k.Bits),
				"Generate a 2048-bit key in your sending service and publish it at "+k.Name+" (or a new selector).")
		case k.KeyType == "rsa" && k.Bits < 2048:
			c.issue("dkim", model.Notice, "DKIM key is 1024 bits",
				k.Name+" holds a 1024-bit RSA key. It still works, but 2048 bits is the current recommendation.",
				"Rotate to a 2048-bit key under a new selector, then retire "+k.Selector+".")
		}
	}
	if !hasRSA(c.rep.DKIM) {
		c.issue("dkim", model.Notice, "DKIM has only Ed25519 keys",
			"Many receivers don't verify Ed25519 signatures yet (RFC 8463), so mail should also carry an RSA signature.",
			"Publish an RSA key alongside the Ed25519 one and sign with both.")
	}
}

func hasRSA(keys []DKIMKey) bool {
	for _, k := range keys {
		if k.KeyType == "rsa" || k.Revoked || k.Problem != "" {
			return true
		}
	}
	return false
}

// dkimKey reads and parses selector._domainkey.domain.
func (c *checker) dkimKey(sel, domain string) (DKIMKey, bool, error) {
	name := sel + "._domainkey." + domain
	k := DKIMKey{Selector: sel, Domain: domain, Name: name}
	recs, err := c.r.LookupTXT(c.ctx, name)
	if err != nil {
		if NotFound(err) {
			return k, false, nil
		}
		return k, false, err
	}
	if cn, err := c.r.LookupCNAME(c.ctx, name); err == nil && normalize(cn) != name {
		k.CNAME = normalize(cn)
	}
	var rec string
	for _, r := range recs {
		r = strings.TrimSpace(r)
		if strings.Contains(r, "p=") || strings.HasPrefix(strings.ToLower(r), "v=dkim1") {
			rec = r
			break
		}
	}
	if rec == "" {
		return k, false, nil
	}
	ParseDKIM(rec, &k)
	return k, true, nil
}

// ParseDKIM fills k's key type, size and problems from a DKIM TXT record.
func ParseDKIM(rec string, k *DKIMKey) {
	t, _ := tags(rec)
	k.KeyType = strings.ToLower(t["k"])
	if k.KeyType == "" {
		k.KeyType = "rsa"
	}
	if v, ok := t["v"]; ok && !strings.EqualFold(v, "DKIM1") {
		k.Problem = "v= must be DKIM1, got " + v
		return
	}
	p, ok := t["p"]
	if !ok {
		k.Problem = "no p= (public key) tag"
		return
	}
	p = strings.Join(strings.Fields(p), "")
	if p == "" {
		k.Revoked = true
		return
	}
	der, err := base64.StdEncoding.DecodeString(p)
	if err != nil {
		k.Problem = "p= is not valid base64"
		return
	}
	switch k.KeyType {
	case "rsa":
		bits, err := rsaBits(der)
		if err != nil {
			k.Problem = "p= is not an RSA public key: " + err.Error()
			return
		}
		k.Bits = bits
	case "ed25519":
		if len(der) != 32 {
			k.Problem = fmt.Sprintf("an Ed25519 key is 32 bytes, p= has %d", len(der))
			return
		}
		k.Bits = 256
	default:
		k.Problem = "unknown key type k=" + k.KeyType
	}
}

// rsaBits returns the modulus size of a DER RSA public key, either a
// SubjectPublicKeyInfo (what DKIM specifies) or a bare PKCS#1 key (what
// some generators publish). It parses the ASN.1 directly so keys too weak
// for crypto/rsa to accept can still be measured and reported.
func rsaBits(der []byte) (int, error) {
	var spki struct {
		Algo struct {
			OID    asn1.ObjectIdentifier
			Params asn1.RawValue `asn1:"optional"`
		}
		Key asn1.BitString
	}
	if rest, err := asn1.Unmarshal(der, &spki); err == nil && len(rest) == 0 {
		if spki.Algo.OID.Equal(oidEd25519) {
			return 0, fmt.Errorf("it is an Ed25519 key; set k=ed25519")
		}
		if !spki.Algo.OID.Equal(oidRSA) {
			return 0, fmt.Errorf("algorithm %v", spki.Algo.OID)
		}
		der = spki.Key.Bytes
	}
	var pk struct {
		N *big.Int
		E int
	}
	if rest, err := asn1.Unmarshal(der, &pk); err != nil || len(rest) != 0 || pk.N == nil || pk.N.Sign() <= 0 {
		return 0, fmt.Errorf("malformed key")
	}
	return pk.N.BitLen(), nil
}
