// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package headers

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/dns"
	"github.com/RhythmShahriar/echo/model"
)

func load(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func titles(r Result) []string {
	out := []string{}
	for _, i := range r.Issues {
		out = append(out, i.Title)
	}
	sort.Strings(out)
	return out
}

func has(r Result, title string) *model.Issue {
	for i := range r.Issues {
		if r.Issues[i].Title == title {
			return &r.Issues[i]
		}
	}
	return nil
}

// Every fixture: who sent it, what Sender feeds dns.Check, and the exact
// issues (so a clean message is the negative case for every check).
func TestProviders(t *testing.T) {
	cases := []struct {
		file   string
		sender dns.Sender
		auth   [3]string // spf, dkim, dmarc
		issues []string
	}{
		{"sendgrid.eml", dns.Sender{Provider: "sendgrid", ReturnPathDomain: "em123.shop.example", DKIMDomain: "shop.example", DKIMSelector: "s1", SMTPHost: "167.89.12.34"},
			[3]string{"pass", "pass", "pass"}, nil},
		{"mailgun.eml", dns.Sender{Provider: "mailgun", ReturnPathDomain: "mg.shop.example", DKIMDomain: "mg.shop.example", DKIMSelector: "smtp", SMTPHost: "159.135.228.12"},
			[3]string{"pass", "pass", "pass"}, nil},
		{"ses.eml", dns.Sender{Provider: "ses", ReturnPathDomain: "amazonses.com", DKIMDomain: "amazonses.com", DKIMSelector: "6gbrjpgwjskckoa6a5zn6fwqkn67xbtw", SMTPHost: "54.240.8.30"},
			[3]string{"pass", "pass", "fail"}, []string{"DMARC failed", "From domain is not aligned with SPF or DKIM"}},
		{"postmark.eml", dns.Sender{Provider: "postmark", ReturnPathDomain: "pm-bounces.shop.example", DKIMDomain: "shop.example", DKIMSelector: "20240101pm", SMTPHost: "50.31.156.113"},
			[3]string{"pass", "pass", "pass"}, nil},
		{"resend.eml", dns.Sender{Provider: "resend", ReturnPathDomain: "send.shop.example", DKIMDomain: "shop.example", DKIMSelector: "resend", SMTPHost: "54.240.48.110"},
			[3]string{"pass", "pass", "pass"}, nil},
		{"brevo.eml", dns.Sender{Provider: "brevo", ReturnPathDomain: "sendibt2.com", DKIMDomain: "shop.example", DKIMSelector: "brevo1", SMTPHost: "185.41.28.109"},
			[3]string{"pass", "pass", "pass"}, []string{"List-Unsubscribe has no one-click option"}},
		{"mailchimp.eml", dns.Sender{Provider: "mailchimp", ReturnPathDomain: "mail134.atl121.rsgsv.net", DKIMDomain: "shop.example", DKIMSelector: "k1", SMTPHost: "198.2.134.218"},
			[3]string{"pass", "pass", "pass"}, nil},
		{"mandrill.eml", dns.Sender{Provider: "mandrill", ReturnPathDomain: "mandrillapp.com", DKIMDomain: "shop.example", DKIMSelector: "mte1", SMTPHost: "198.2.187.15"},
			[3]string{"pass", "pass", "pass"}, nil},
		{"sparkpost.eml", dns.Sender{Provider: "sparkpost", ReturnPathDomain: "bounces.shop.example", DKIMDomain: "shop.example", DKIMSelector: "scph0124", SMTPHost: "192.174.90.23"},
			[3]string{"softfail", "pass", "pass"}, []string{"SPF softfail"}},
		{"google.eml", dns.Sender{Provider: "google", ReturnPathDomain: "shop.example", DKIMDomain: "shop.example", DKIMSelector: "google", SMTPHost: "209.85.220.41"},
			[3]string{"pass", "pass", "pass"}, nil},
		{"outlook.eml", dns.Sender{Provider: "microsoft", ReturnPathDomain: "shop.example", DKIMDomain: "shopexample.onmicrosoft.com", DKIMSelector: "selector1-shopexample-onmicrosoft-com", SMTPHost: "40.107.22.101"},
			[3]string{"pass", "pass", "pass"}, []string{"DKIM is not aligned with the From domain"}},
		{"phpmail.eml", dns.Sender{Provider: "php-mail", ReturnPathDomain: "web01.shop.example", SMTPHost: "203.0.113.7"},
			[3]string{"softfail", "", "fail"}, []string{"DMARC failed", "Mail header leaks a server script path", "Mail header reveals the PHP version",
				"Message is not DKIM-signed", "SPF softfail", "Sent with PHP mail() from the web server"}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			r := Analyze(load(t, tc.file))
			if r.Sender != tc.sender {
				t.Errorf("sender =\n %+v\nwant\n %+v", r.Sender, tc.sender)
			}
			if got := [3]string{r.Auth.SPF, r.Auth.DKIM, r.Auth.DMARC}; got != tc.auth {
				t.Errorf("auth = %v", got)
			}
			want := append([]string{}, tc.issues...)
			if want == nil {
				want = []string{}
			}
			sort.Strings(want)
			if got := titles(r); !reflect.DeepEqual(got, want) {
				t.Errorf("issues = %q\nwant %q", got, want)
			}
			for _, i := range r.Issues {
				if i.Fix == "" || i.Detail == "" || i.Key != i.Check+"|"+i.Title || !strings.HasPrefix(i.Check, "headers/") {
					t.Errorf("issue %+v", i)
				}
			}
			if r.FromDomain != "shop.example" || r.Auth.Server != "mx.google.com" || r.Provider == "" || r.MessageID == "" {
				t.Errorf("result = %+v", r)
			}
		})
	}
}

func TestHopsAndSignatures(t *testing.T) {
	r := Analyze(load(t, "phpmail.eml"))
	if len(r.Hops) != 3 {
		t.Fatalf("hops = %+v", r.Hops)
	}
	first, mx := r.Hops[0], r.Hops[1]
	if first.By != "web01.shop.example" || first.From != "" || !strings.Contains(first.Raw, "from userid 33") {
		t.Errorf("first hop = %+v", first)
	}
	if mx.From != "web01.shop.example" || mx.IP != "203.0.113.7" || mx.By != "mx.google.com" || mx.With != "ESMTPS" || mx.Delay != 1 {
		t.Errorf("mx hop = %+v", mx)
	}
	r = Analyze(load(t, "sendgrid.eml"))
	if len(r.Signatures) != 2 || r.Signatures[0].Domain != "shop.example" || r.Signatures[0].Algorithm != "rsa-sha256" || r.Signatures[0].Headers[0] != "content-type" {
		t.Errorf("signatures = %+v", r.Signatures)
	}
	if len(r.Auth.Methods) != 4 || r.Auth.prop("spf", "smtp.mailfrom") != "bounces+4815162-ab12-jamie=example.net@em123.shop.example" {
		t.Errorf("methods = %+v", r.Auth.Methods)
	}
	if r := Analyze(load(t, "mailchimp.eml")); r.Subject != "Autumn collection — just landed" {
		t.Errorf("subject = %q", r.Subject)
	}
}

// edit replaces or removes (value "") one header line of a fixture.
func edit(raw []byte, name, value string) []byte {
	var out []string
	lines := strings.Split(string(raw), "\n")
	skip := false
	done := false
	for _, l := range lines {
		if skip && (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")) {
			continue
		}
		skip = false
		if !done && strings.HasPrefix(strings.ToLower(l), strings.ToLower(name)+":") {
			skip, done = true, true
			if value != "" {
				out = append(out, name+": "+value)
			}
			continue
		}
		out = append(out, l)
	}
	if !done && value != "" {
		out = append([]string{name + ": " + value}, out...)
	}
	return []byte(strings.Join(out, "\n"))
}

func TestIssues(t *testing.T) {
	sg := func(t *testing.T) []byte { return load(t, "sendgrid.eml") }
	ar := func(v string) func(t *testing.T) []byte {
		return func(t *testing.T) []byte { return edit(sg(t), "Authentication-Results", v) }
	}
	sig := func(v string) func(t *testing.T) []byte {
		return func(t *testing.T) []byte {
			b := edit(sg(t), "DKIM-Signature", "")
			return edit(b, "DKIM-Signature", v)
		}
	}
	cases := []struct {
		name  string
		raw   func(t *testing.T) []byte
		title string
		level model.Level
	}{
		{"spf fail", ar("mx.google.com; dkim=pass header.d=shop.example; spf=fail smtp.mailfrom=x@em123.shop.example; dmarc=pass header.from=shop.example"), "SPF failed", model.Error},
		{"spf neutral", ar("mx.google.com; dkim=pass; spf=neutral; dmarc=pass"), "SPF result is neutral", model.Notice},
		{"spf none", ar("mx.google.com; dkim=pass; spf=none; dmarc=pass"), "SPF found no record", model.Warning},
		{"spf permerror", ar("mx.google.com; dkim=pass; spf=permerror; dmarc=pass"), "SPF record is broken (permerror)", model.Error},
		{"spf temperror", ar("mx.google.com; dkim=pass; spf=temperror; dmarc=pass"), "SPF could not be evaluated", model.Warning},
		{"dkim fail", ar("mx.google.com; dkim=fail (body hash did not verify) header.d=shop.example header.s=s1; spf=pass; dmarc=pass"), "DKIM signature failed", model.Error},
		{"dmarc permerror", ar("mx.google.com; dkim=pass; spf=pass; dmarc=permerror"), "DMARC could not be evaluated", model.Warning},
		{"dmarc none", ar("mx.google.com; dkim=pass; spf=pass; dmarc=none header.from=shop.example"), "Receiver found no DMARC policy", model.Warning},
		{"no auth results", func(t *testing.T) []byte {
			b := edit(sg(t), "Authentication-Results", "")
			return edit(b, "ARC-Authentication-Results", "")
		}, "No Authentication-Results header", model.Notice},
		{"sha1", sig("v=1; a=rsa-sha1; d=shop.example; s=s1; h=from:to:subject; bh=x; b=y"), "DKIM signature uses SHA-1", model.Warning},
		{"body length", sig("v=1; a=rsa-sha256; d=shop.example; s=s1; l=120; h=from:to; bh=x; b=y"), "DKIM signature covers only part of the body", model.Notice},
		{"from not signed", sig("v=1; a=rsa-sha256; d=shop.example; s=s1; h=to:subject; bh=x; b=y"), "DKIM signature does not cover From", model.Error},
		{"no message id", func(t *testing.T) []byte { return edit(sg(t), "Message-ID", "") }, "Message has no Message-ID", model.Warning},
		{"no date", func(t *testing.T) []byte { return edit(sg(t), "Date", "") }, "Message has no Date", model.Warning},
		{"bad from", func(t *testing.T) []byte { return edit(sg(t), "From", "not an address") }, "From header is missing or invalid", model.Error},
		{"bulk without unsubscribe", func(t *testing.T) []byte { return edit(load(t, "mailchimp.eml"), "List-Unsubscribe", "") }, "Bulk mail has no List-Unsubscribe header", model.Notice},
		{"precedence bulk", func(t *testing.T) []byte {
			return edit(edit(sg(t), "List-Unsubscribe", ""), "Precedence", "bulk")
		}, "Bulk mail has no List-Unsubscribe header", model.Notice},
		{"unparseable", func(*testing.T) []byte { return []byte("this is not\na message") }, "Message cannot be parsed", model.Error},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Analyze(tc.raw(t))
			i := has(r, tc.title)
			if i == nil {
				t.Fatalf("want %q, got %q", tc.title, titles(r))
			}
			if i.Level != tc.level || i.Fix == "" {
				t.Errorf("issue = %+v", i)
			}
		})
	}
}

func TestARCFallback(t *testing.T) {
	raw := edit(load(t, "sendgrid.eml"), "Authentication-Results", "")
	raw = edit(raw, "ARC-Authentication-Results", "i=2; mx.example.net; dkim=fail header.d=shop.example; spf=softfail; dmarc=fail")
	raw = append([]byte("ARC-Authentication-Results: i=1; mx.relay.example; dkim=pass; spf=pass; dmarc=pass\n"), raw...)
	r := Analyze(raw)
	if !r.Auth.ARC || r.Auth.Server != "mx.example.net" || r.Auth.DMARC != "fail" {
		t.Fatalf("auth = %+v", r.Auth)
	}
	if i := has(r, "DMARC failed"); i == nil || !strings.Contains(i.Detail, "(ARC)") {
		t.Errorf("issues = %q", titles(r))
	}
}

func TestCommentsAndQuotes(t *testing.T) {
	a := parseAR(`mx.example.net 1; spf=pass (sender "IP" is 192.0.2.1; looks fine) smtp.mailfrom="a;b@shop.example"; dkim=pass (2048-bit key) header.d=shop.example`, false)
	if a.SPF != "pass" || a.DKIM != "pass" || a.prop("spf", "smtp.mailfrom") != "a;b@shop.example" || a.prop("dkim", "header.d") != "shop.example" {
		t.Errorf("auth = %+v", a)
	}
}

// The inferred sender plugs into dns.Check: php-mail's relay IP must be in
// SPF, and with no DKIM the check names the alignment failure.
func TestSenderFeedsDNSCheck(t *testing.T) {
	r := Analyze(load(t, "phpmail.eml"))
	z := &dns.Zone{TXT: map[string][]string{
		"shop.example":        {"v=spf1 include:_spf.google.com ~all"},
		"web01.shop.example":  {"v=spf1 a -all"},
		"_spf.google.com":     {"v=spf1 ip4:35.190.247.0/24 ~all"},
		"_dmarc.shop.example": {"v=DMARC1; p=none; rua=mailto:d@shop.example"},
	}, Host: map[string][]string{"web01.shop.example": {"203.0.113.9"}}, MX: map[string][]*net.MX{}}
	rep := dns.Check(context.Background(), r.FromDomain, dns.Options{Resolver: z, Sender: &r.Sender})
	found := false
	for _, i := range rep.Issues {
		if i.Title == "SPF does not authorize the sending service" && strings.Contains(i.Fix, "ip4:203.0.113.7") && strings.Contains(i.Fix, "web01.shop.example") {
			found = true
		}
	}
	if !found {
		t.Errorf("dns issues = %+v", rep.Issues)
	}
}
