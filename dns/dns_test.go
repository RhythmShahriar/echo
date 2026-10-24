// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package dns

import (
	"context"
	"crypto/tls"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/RhythmShahriar/echo/model"
)

// rsaKey returns a base64 SubjectPublicKeyInfo with a modulus of the given
// size. The modulus isn't a real RSA modulus (only its length matters to
// the check), which lets the tests cover sizes crypto/rsa won't generate.
func rsaKey(bits int) string {
	n := new(big.Int).SetBit(new(big.Int), bits-1, 1)
	n.SetBit(n, 0, 1)
	pub, _ := asn1.Marshal(struct {
		N *big.Int
		E int
	}{n, 65537})
	spki, _ := asn1.Marshal(struct {
		Algo pkix.AlgorithmIdentifier
		Key  asn1.BitString
	}{pkix.AlgorithmIdentifier{Algorithm: asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}, Parameters: asn1.NullRawValue},
		asn1.BitString{Bytes: pub, BitLength: len(pub) * 8}})
	return base64.StdEncoding.EncodeToString(spki)
}

func dkimRec(bits int) string { return "v=DKIM1; k=rsa; p=" + rsaKey(bits) }

// clean is a domain with every record right, sending through SendGrid with
// domain authentication (em123 bounce subdomain, s1/s2 CNAMEs).
func clean() *Zone {
	return &Zone{
		TXT: map[string][]string{
			"shop.example":                      {"v=spf1 include:sendgrid.net include:_spf.google.com ~all", "google-site-verification=abc"},
			"sendgrid.net":                      {"v=spf1 ip4:167.89.0.0/17 ip4:208.117.48.0/20 ~all"},
			"_spf.google.com":                   {"v=spf1 include:_netblocks.google.com ~all"},
			"_netblocks.google.com":             {"v=spf1 ip4:35.190.247.0/24 ip4:64.233.160.0/19 ~all"},
			"em123.shop.example":                {"v=spf1 include:sendgrid.net ~all"},
			"s1.domainkey.u123.wl.sendgrid.net": {dkimRec(2048)},
			"s2.domainkey.u123.wl.sendgrid.net": {dkimRec(2048)},
			"_dmarc.shop.example":               {"v=DMARC1; p=reject; rua=mailto:dmarc@shop.example; adkim=r; aspf=r"},
			"_mta-sts.shop.example":             {"v=STSv1; id=20261001"},
			"_smtp._tls.shop.example":           {"v=TLSRPTv1; rua=mailto:tls@shop.example"},
			"default._bimi.shop.example":        {"v=BIMI1; l=https://shop.example/logo.svg"},
		},
		CNAME: map[string]string{
			"s1._domainkey.shop.example": "s1.domainkey.u123.wl.sendgrid.net",
			"s2._domainkey.shop.example": "s2.domainkey.u123.wl.sendgrid.net",
		},
		MX: map[string][]*net.MX{
			"shop.example": {{Host: "aspmx.l.google.com.", Pref: 1}, {Host: "alt1.aspmx.l.google.com.", Pref: 5}},
		},
		Host: map[string][]string{
			"aspmx.l.google.com":      {"142.250.0.27"},
			"alt1.aspmx.l.google.com": {"142.250.1.27"},
		},
	}
}

func sendgrid() *Sender {
	return &Sender{Provider: "sendgrid", ReturnPathDomain: "em123.shop.example", DKIMDomain: "shop.example", DKIMSelector: "s1"}
}

func run(z *Zone, s *Sender) Report {
	return Check(context.Background(), "shop.example", Options{Resolver: z, Sender: s})
}

func titles(r Report) []string {
	var out []string
	for _, i := range r.Issues {
		out = append(out, i.Title)
	}
	return out
}

func find(r Report, title string) *model.Issue {
	for i := range r.Issues {
		if r.Issues[i].Title == title {
			return &r.Issues[i]
		}
	}
	return nil
}

func TestCleanDomainHasNoIssues(t *testing.T) {
	for _, s := range []*Sender{nil, sendgrid()} {
		r := run(clean(), s)
		if len(r.Issues) != 0 {
			t.Fatalf("sender %v: issues on a clean domain: %q", s, titles(r))
		}
		if r.SPF == nil || r.SPF.Lookups != 3 || r.SPF.All != "~all" {
			t.Errorf("spf facts = %+v", r.SPF)
		}
		if r.DMARC == nil || r.DMARC.Policy != "reject" || r.MTASTS == nil || r.TLSRPT == "" || r.BIMI == "" || len(r.MX) != 2 {
			t.Errorf("facts = %+v", r)
		}
	}
	r := run(clean(), sendgrid())
	if len(r.DKIM) != 1 || r.DKIM[0].Bits != 2048 || r.DKIM[0].CNAME != "s1.domainkey.u123.wl.sendgrid.net" {
		t.Errorf("dkim = %+v", r.DKIM)
	}
	if r.OrgDomain != "shop.example" {
		t.Errorf("org = %q", r.OrgDomain)
	}
}

// Each case breaks the clean zone one way and expects one stable title.
func TestIssues(t *testing.T) {
	cases := []struct {
		name   string
		edit   func(z *Zone)
		sender *Sender
		title  string
		level  model.Level
	}{
		{"spf missing", func(z *Zone) { z.TXT["shop.example"] = []string{"google-site-verification=abc"} }, nil, "No SPF record", model.Error},
		{"spf multiple", func(z *Zone) { z.TXT["shop.example"] = []string{"v=spf1 include:sendgrid.net ~all", "v=spf1 mx -all"} }, nil, "Multiple SPF records", model.Error},
		{"spf +all", func(z *Zone) { z.TXT["shop.example"] = []string{"v=spf1 include:sendgrid.net +all"} }, nil, "SPF allows every server (+all)", model.Error},
		{"spf bare all", func(z *Zone) { z.TXT["shop.example"] = []string{"v=spf1 include:sendgrid.net all"} }, nil, "SPF allows every server (+all)", model.Error},
		{"spf ?all", func(z *Zone) { z.TXT["shop.example"] = []string{"v=spf1 include:sendgrid.net ?all"} }, nil, "SPF is neutral for every other server (?all)", model.Warning},
		{"spf no all", func(z *Zone) { z.TXT["shop.example"] = []string{"v=spf1 include:sendgrid.net"} }, nil, "SPF record has no all mechanism", model.Warning},
		{"spf syntax", func(z *Zone) { z.TXT["shop.example"] = []string{"v=spf1 ip4:300.1.1.1 inclde:sendgrid.net ~all"} }, nil, "SPF record has a syntax error", model.Error},
		{"spf missing include", func(z *Zone) { z.TXT["shop.example"] = []string{"v=spf1 include:gone.example ~all"} }, nil, "SPF includes a domain without an SPF record", model.Error},
		{"spf loop", func(z *Zone) {
			z.TXT["shop.example"] = []string{"v=spf1 include:a.example ~all"}
			z.TXT["a.example"] = []string{"v=spf1 include:shop.example ~all"}
		}, nil, "SPF includes loop", model.Error},
		{"spf ptr", func(z *Zone) { z.TXT["shop.example"] = []string{"v=spf1 ptr include:sendgrid.net ~all"} }, nil, "SPF uses the ptr mechanism", model.Notice},
		{"spf voids", func(z *Zone) {
			z.TXT["shop.example"] = []string{"v=spf1 a:x1.shop.example a:x2.shop.example a:x3.shop.example include:sendgrid.net ~all"}
		}, nil, "SPF has more than 2 void lookups", model.Error},
		{"spf provider missing", func(z *Zone) {
			z.TXT["shop.example"] = []string{"v=spf1 include:_spf.google.com ~all"}
			delete(z.TXT, "em123.shop.example")
		}, &Sender{Provider: "mailgun", DKIMDomain: "shop.example"}, "SPF does not authorize the sending service", model.Error},
		{"spf bounce domain", func(z *Zone) { delete(z.TXT, "em123.shop.example") }, sendgrid(), "Bounce domain has no SPF record", model.Warning},
		{"spf php server ip", nil, &Sender{Provider: "php-mail", ReturnPathDomain: "shop.example", SMTPHost: "198.51.100.7"},
			"SPF does not authorize the sending service", model.Error},
		{"spf lookup failed", func(z *Zone) { z.Fail = map[string]bool{"shop.example": true} }, nil, "SPF lookup failed", model.Warning},

		{"dkim selector missing", nil, &Sender{Provider: "postmark", DKIMDomain: "shop.example", DKIMSelector: "20240101pm", ReturnPathDomain: "pm-bounces.shop.example"},
			"DKIM key not found for the sending selector", model.Error},
		{"dkim provider defaults missing", func(z *Zone) { z.CNAME = nil }, &Sender{Provider: "sendgrid"}, "No DKIM key found for the sending service", model.Warning},
		{"dkim common missing", func(z *Zone) { z.CNAME = nil }, nil, "No DKIM key found at common selectors", model.Notice},
		{"dkim ses unknown selector", func(z *Zone) { z.CNAME = nil }, &Sender{Provider: "ses"}, "DKIM selector unknown for the sending service", model.Notice},
		{"dkim short", func(z *Zone) { z.TXT["s1.domainkey.u123.wl.sendgrid.net"] = []string{dkimRec(512)} }, sendgrid(), "DKIM key is shorter than 1024 bits", model.Error},
		{"dkim 1024", func(z *Zone) { z.TXT["s1.domainkey.u123.wl.sendgrid.net"] = []string{dkimRec(1024)} }, sendgrid(), "DKIM key is 1024 bits", model.Notice},
		{"dkim revoked", func(z *Zone) { z.TXT["s1.domainkey.u123.wl.sendgrid.net"] = []string{"v=DKIM1; k=rsa; p="} }, sendgrid(), "DKIM key is revoked", model.Error},
		{"dkim bad base64", func(z *Zone) { z.TXT["s1.domainkey.u123.wl.sendgrid.net"] = []string{"v=DKIM1; p=!!notbase64"} }, sendgrid(), "DKIM record is invalid", model.Error},
		{"dkim ed25519 only", func(z *Zone) {
			z.TXT["s1.domainkey.u123.wl.sendgrid.net"] = []string{"v=DKIM1; k=ed25519; p=" + base64.StdEncoding.EncodeToString(make([]byte, 32))}
		}, sendgrid(), "DKIM has only Ed25519 keys", model.Notice},

		{"dmarc missing", func(z *Zone) { delete(z.TXT, "_dmarc.shop.example") }, nil, "No DMARC record", model.Error},
		{"dmarc multiple", func(z *Zone) { z.TXT["_dmarc.shop.example"] = []string{"v=DMARC1; p=none", "v=DMARC1; p=reject"} }, nil, "Multiple DMARC records", model.Error},
		{"dmarc none", func(z *Zone) { z.TXT["_dmarc.shop.example"] = []string{"v=DMARC1; p=none; rua=mailto:d@shop.example"} }, nil, "DMARC policy is none", model.Notice},
		{"dmarc no rua", func(z *Zone) { z.TXT["_dmarc.shop.example"] = []string{"v=DMARC1; p=reject"} }, nil, "DMARC sends no aggregate reports", model.Warning},
		{"dmarc pct", func(z *Zone) {
			z.TXT["_dmarc.shop.example"] = []string{"v=DMARC1; p=quarantine; pct=25; rua=mailto:d@shop.example"}
		}, nil, "DMARC policy applies to only part of the mail", model.Notice},
		{"dmarc sp none", func(z *Zone) {
			z.TXT["_dmarc.shop.example"] = []string{"v=DMARC1; p=reject; sp=none; rua=mailto:d@shop.example"}
		}, nil, "DMARC leaves subdomains unprotected", model.Notice},
		{"dmarc invalid", func(z *Zone) { z.TXT["_dmarc.shop.example"] = []string{"v=DMARC1; p=block; rua=mailto:d@shop.example"} }, nil, "DMARC record is invalid", model.Error},
		{"dmarc rua unauthorized", func(z *Zone) {
			z.TXT["_dmarc.shop.example"] = []string{"v=DMARC1; p=reject; rua=mailto:r@reports.example"}
		}, nil, "DMARC report address hasn't authorized reports", model.Warning},
		{"dmarc unaligned", nil, &Sender{Provider: "sendgrid", ReturnPathDomain: "bounces.sendgrid.net", DKIMDomain: "sendgrid.net"},
			"Mail from the sending service fails DMARC alignment", model.Error},
		{"dmarc strict", func(z *Zone) {
			z.TXT["_dmarc.shop.example"] = []string{"v=DMARC1; p=reject; adkim=s; aspf=s; rua=mailto:d@shop.example"}
		},
			&Sender{Provider: "mailgun", ReturnPathDomain: "mg.shop.example", DKIMDomain: "mg.shop.example"}, "DMARC strict alignment fails for the sending service", model.Error},
		{"dmarc spf only", nil, &Sender{Provider: "sendgrid", ReturnPathDomain: "em123.shop.example", DKIMDomain: "sendgrid.net"}, "DMARC passes on SPF alone", model.Notice},

		{"mx missing", func(z *Zone) { delete(z.MX, "shop.example") }, nil, "No MX record", model.Warning},
		{"mx null", func(z *Zone) { z.MX["shop.example"] = []*net.MX{{Host: ".", Pref: 0}} }, sendgrid(), "Domain has a null MX", model.Warning},
		{"mx dead", func(z *Zone) { delete(z.Host, "alt1.aspmx.l.google.com") }, nil, "MX host does not resolve", model.Warning},
		{"mta-sts missing", func(z *Zone) { delete(z.TXT, "_mta-sts.shop.example") }, nil, "No MTA-STS policy", model.Notice},
		{"mta-sts no id", func(z *Zone) { z.TXT["_mta-sts.shop.example"] = []string{"v=STSv1;"} }, nil, "MTA-STS record has no id", model.Warning},
		{"tls-rpt missing", func(z *Zone) { delete(z.TXT, "_smtp._tls.shop.example") }, nil, "No TLS-RPT record", model.Notice},
		{"tls-rpt no rua", func(z *Zone) { z.TXT["_smtp._tls.shop.example"] = []string{"v=TLSRPTv1;"} }, nil, "TLS-RPT record has no rua", model.Warning},
		{"bimi missing", func(z *Zone) { delete(z.TXT, "default._bimi.shop.example") }, nil, "No BIMI record", model.Notice},
		{"bimi without enforcement", func(z *Zone) { z.TXT["_dmarc.shop.example"] = []string{"v=DMARC1; p=none; rua=mailto:d@shop.example"} }, nil, "BIMI needs DMARC enforcement", model.Notice},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			z := clean()
			if tc.edit != nil {
				tc.edit(z)
			}
			r := run(z, tc.sender)
			i := find(r, tc.title)
			if i == nil {
				t.Fatalf("want %q, got %q", tc.title, titles(r))
			}
			if i.Level != tc.level || i.Fix == "" || i.Detail == "" || i.Key != i.Check+"|"+i.Title || !strings.HasPrefix(i.Check, "dns/") {
				t.Errorf("issue = %+v", i)
			}
		})
	}
}

func TestSPFLookupLimitCountsNestedIncludes(t *testing.T) {
	z := clean()
	var incs []string
	for i := 0; i < 4; i++ {
		name := fmt.Sprintf("n%d.example", i)
		incs = append(incs, "include:"+name)
		z.TXT[name] = []string{fmt.Sprintf("v=spf1 include:m%d.example a mx ~all", i)}
		z.TXT[fmt.Sprintf("m%d.example", i)] = []string{"v=spf1 ip4:192.0.2.0/24 -all"}
		z.Host[name] = []string{"192.0.2.1"}
		z.MX[name] = []*net.MX{{Host: "aspmx.l.google.com.", Pref: 1}}
	}
	z.TXT["shop.example"] = []string{"v=spf1 " + strings.Join(incs, " ") + " ~all"}
	r := run(z, nil)
	// 4 includes, each costing include + nested include + a + mx = 16.
	if r.SPF.Lookups != 16 {
		t.Errorf("lookups = %d", r.SPF.Lookups)
	}
	i := find(r, "SPF needs more than 10 DNS lookups")
	if i == nil || !strings.Contains(i.Detail, "16 DNS lookups") {
		t.Fatalf("issues = %q", titles(r))
	}
	// Exactly 10 is allowed.
	z.TXT["shop.example"] = []string{"v=spf1 include:n0.example include:n1.example a a ~all"}
	z.Host["shop.example"] = []string{"192.0.2.9"}
	r = run(z, nil)
	if r.SPF.Lookups != 10 || find(r, "SPF needs more than 10 DNS lookups") != nil {
		t.Errorf("10 lookups: %d %q", r.SPF.Lookups, titles(r))
	}
}

func TestSPFRedirectAndMacros(t *testing.T) {
	z := clean()
	z.TXT["shop.example"] = []string{"v=spf1 exists:%{i}._spf.shop.example redirect=_spf.shop.example"}
	z.TXT["_spf.shop.example"] = []string{"v=spf1 include:sendgrid.net -all"}
	r := run(z, nil)
	if r.SPF.All != "-all" || r.SPF.Lookups != 3 || len(r.Issues) != 0 {
		t.Errorf("spf = %+v, issues %q", r.SPF, titles(r))
	}
}

func TestSPFAuthorizesServerIP(t *testing.T) {
	z := clean()
	z.TXT["shop.example"] = []string{"v=spf1 ip4:198.51.100.0/24 a:mail.shop.example ~all"}
	z.Host["mail.shop.example"] = []string{"203.0.113.25"}
	for _, host := range []string{"198.51.100.7", "203.0.113.25", "mail.shop.example"} {
		r := run(z, &Sender{Provider: "php-mail", ReturnPathDomain: "shop.example", SMTPHost: host})
		if i := find(r, "SPF does not authorize the sending service"); i != nil {
			t.Errorf("%s: %+v", host, i)
		}
	}
	r := run(z, &Sender{Provider: "php-mail", ReturnPathDomain: "shop.example", SMTPHost: "192.0.2.50"})
	if i := find(r, "SPF does not authorize the sending service"); i == nil || !strings.Contains(i.Fix, "ip4:192.0.2.50") {
		t.Errorf("unlisted IP: %q", titles(r))
	}
}

func TestSPFProviderIncludeNested(t *testing.T) {
	z := clean()
	z.TXT["shop.example"] = []string{"v=spf1 include:_spf.shop.example ~all"}
	z.TXT["_spf.shop.example"] = []string{"v=spf1 include:mailgun.org -all"}
	z.TXT["mailgun.org"] = []string{"v=spf1 ip4:69.72.32.0/20 -all"}
	r := run(z, &Sender{Provider: "mailgun", ReturnPathDomain: "shop.example", DKIMDomain: "shop.example", DKIMSelector: "s1"})
	if find(r, "SPF does not authorize the sending service") != nil {
		t.Errorf("nested include not seen: %q", titles(r))
	}
}

func TestDMARCInheritsFromOrgDomain(t *testing.T) {
	z := clean()
	z.TXT["_dmarc.shop.example"] = []string{"v=DMARC1; p=reject; sp=quarantine; rua=mailto:d@shop.example"}
	z.MX["news.shop.example"] = z.MX["shop.example"]
	z.TXT["news.shop.example"] = []string{"v=spf1 include:sendgrid.net ~all"}
	r := Check(context.Background(), "News.Shop.Example.", Options{Resolver: z})
	if r.Domain != "news.shop.example" || r.DMARC == nil || !r.DMARC.Inherited || r.DMARC.Policy != "quarantine" {
		t.Fatalf("dmarc = %+v", r.DMARC)
	}
	if find(r, "No DMARC record") != nil {
		t.Errorf("issues = %q", titles(r))
	}
}

func TestDMARCExternalReportsAuthorized(t *testing.T) {
	z := clean()
	z.TXT["_dmarc.shop.example"] = []string{"v=DMARC1; p=reject; rua=mailto:r@reports.example"}
	z.TXT["shop.example._report._dmarc.reports.example"] = []string{"v=DMARC1"}
	if r := run(z, nil); len(r.Issues) != 0 {
		t.Errorf("issues = %q", titles(r))
	}
}

func TestDKIMSESCNAME(t *testing.T) {
	z := clean()
	z.CNAME["abcdef123._domainkey.shop.example"] = "abcdef123.dkim.amazonses.com"
	z.TXT["abcdef123.dkim.amazonses.com"] = []string{"p=" + rsaKey(2048)}
	r := run(z, &Sender{Provider: "ses", DKIMDomain: "shop.example", DKIMSelector: "abcdef123", ReturnPathDomain: "shop.example"})
	if len(r.DKIM) != 1 || r.DKIM[0].CNAME != "abcdef123.dkim.amazonses.com" || r.DKIM[0].Bits != 2048 {
		t.Errorf("dkim = %+v", r.DKIM)
	}
	for _, i := range r.Issues {
		if i.Check == "dns/dkim" {
			t.Errorf("unexpected %+v", i)
		}
	}
}

func TestParseDKIMPKCS1(t *testing.T) {
	der, _ := base64.StdEncoding.DecodeString(rsaKey(2048))
	var spki struct {
		Algo pkix.AlgorithmIdentifier
		Key  asn1.BitString
	}
	if _, err := asn1.Unmarshal(der, &spki); err != nil {
		t.Fatal(err)
	}
	var k DKIMKey
	ParseDKIM("p="+base64.StdEncoding.EncodeToString(spki.Key.Bytes), &k)
	if k.Bits != 2048 || k.Problem != "" {
		t.Errorf("pkcs1 = %+v", k)
	}
}

// pipeListener hands out in-memory connections, so the HTTPS policy test
// needs no network at all (it passes under unshare -rn, loopback down).
type pipeListener struct {
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}
func (l *pipeListener) Close() error   { l.once.Do(func() { close(l.done) }); return nil }
func (l *pipeListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 443} }

func (l *pipeListener) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case l.conns <- server:
		return client, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func policyServer(t *testing.T, status int, body string) *http.Client {
	t.Helper()
	l := &pipeListener{conns: make(chan net.Conn), done: make(chan struct{})}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "mta-sts.shop.example" || r.URL.Path != "/.well-known/mta-sts.txt" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	_ = srv.Listener.Close()
	srv.Listener = l
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		DialContext:     l.dial,
	}}
}

func TestMTASTSPolicy(t *testing.T) {
	good := "version: STSv1\nmode: enforce\nmx: aspmx.l.google.com\nmx: *.aspmx.l.google.com\nmax_age: 604800\n"
	cases := []struct {
		status int
		body   string
		title  string
	}{
		{200, good, ""},
		{404, "", "MTA-STS policy file cannot be fetched"},
		{200, "version: STSv1\nmode: sometimes\n", "MTA-STS policy file is invalid"},
		{200, strings.Replace(good, "enforce", "testing", 1), "MTA-STS is in testing mode"},
		{200, "version: STSv1\nmode: enforce\nmx: aspmx.l.google.com\nmax_age: 86400\n", "MTA-STS policy does not cover the MX hosts"},
	}
	for _, tc := range cases {
		r := Check(context.Background(), "shop.example", Options{Resolver: clean(), HTTP: policyServer(t, tc.status, tc.body)})
		if tc.title == "" {
			if len(r.Issues) != 0 || r.MTASTS.Mode != "enforce" || len(r.MTASTS.MX) != 2 {
				t.Errorf("good policy: %q %+v", titles(r), r.MTASTS)
			}
			continue
		}
		if find(r, tc.title) == nil {
			t.Errorf("want %q, got %q", tc.title, titles(r))
		}
	}
}

func TestOrgDomainAndAlignment(t *testing.T) {
	for in, want := range map[string]string{
		"mail.shop.example.co.uk": "example.co.uk",
		"EM123.Shop.Example.":     "shop.example",
		"bounces.sendgrid.net":    "sendgrid.net",
	} {
		if got := OrgDomain(in); got != want {
			t.Errorf("OrgDomain(%q) = %q", in, got)
		}
	}
	if !Aligned("mg.shop.example", "shop.example", false) || Aligned("mg.shop.example", "shop.example", true) ||
		Aligned("sendgrid.net", "shop.example", false) || Aligned("", "shop.example", false) {
		t.Error("alignment")
	}
}

func TestNoNetworkResolverErrorsAreReported(t *testing.T) {
	z := clean()
	z.Fail = map[string]bool{"_dmarc.shop.example": true, "shop.example": true}
	r := run(z, nil)
	for _, want := range []string{"SPF lookup failed", "DMARC lookup failed", "MX lookup failed"} {
		if find(r, want) == nil {
			t.Errorf("want %q in %q", want, titles(r))
		}
	}
}
