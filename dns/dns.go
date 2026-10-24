// Package dns checks a domain's mail deliverability records (SPF, DKIM,
// DMARC, MX, MTA-STS, TLS-RPT and BIMI) without sending anything. Every
// lookup goes through a Resolver, so checks run against fixture zones in
// tests and against the system resolver in production. The only request
// that is not a DNS query is the optional MTA-STS policy fetch, a GET to
// the domain's own mta-sts host, made only when Options.HTTP is set.
package dns

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sort"
	"strings"

	"golang.org/x/net/publicsuffix"

	"github.com/RhythmShahriar/echo/model"
)

// Resolver is the subset of *net.Resolver the checks need. net.DefaultResolver
// satisfies it; tests use Zone.
type Resolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
	LookupMX(ctx context.Context, name string) ([]*net.MX, error)
	LookupHost(ctx context.Context, host string) ([]string, error)
	LookupCNAME(ctx context.Context, host string) (string, error)
}

// System is the default resolver: the operating system's, through Go's net
// package.
var System Resolver = net.DefaultResolver

// Sender is the service that actually sends the domain's mail, when known
// (from captured mail headers or the site's mailer settings). It turns
// generic checks into specific ones: "does SPF authorize SendGrid", "is
// the s1 key published", "does this mail align with DMARC".
type Sender struct {
	Provider         string `json:"provider,omitempty"`           // key from Providers: sendgrid, mailgun, ses, php-mail…
	ReturnPathDomain string `json:"return_path_domain,omitempty"` // envelope sender domain (what SPF checks)
	DKIMDomain       string `json:"dkim_domain,omitempty"`        // d= of the signature
	DKIMSelector     string `json:"dkim_selector,omitempty"`      // s= of the signature
	SMTPHost         string `json:"smtp_host,omitempty"`          // the relay that handed mail out: a host name or IP
}

// Options configures Check.
type Options struct {
	Resolver Resolver     // nil: System
	Sender   *Sender      // nil: unknown, generic checks only
	HTTP     *http.Client // non-nil: also fetch the MTA-STS policy file over HTTPS
}

// Report is what Check found: the parsed records as facts, and the issues.
type Report struct {
	Domain    string        `json:"domain"`
	OrgDomain string        `json:"org_domain"`
	Sender    *Sender       `json:"sender,omitempty"`
	SPF       *SPF          `json:"spf,omitempty"`
	DKIM      []DKIMKey     `json:"dkim,omitempty"`
	DMARC     *DMARC        `json:"dmarc,omitempty"`
	MX        []string      `json:"mx,omitempty"`
	NullMX    bool          `json:"null_mx,omitempty"`
	MTASTS    *MTASTS       `json:"mta_sts,omitempty"`
	TLSRPT    string        `json:"tls_rpt,omitempty"`
	BIMI      string        `json:"bimi,omitempty"`
	Issues    []model.Issue `json:"issues"`
}

// Check runs every deliverability check for domain.
func Check(ctx context.Context, domain string, o Options) Report {
	domain = normalize(domain)
	c := &checker{ctx: ctx, r: o.Resolver, o: o, rep: Report{Domain: domain, OrgDomain: OrgDomain(domain), Sender: o.Sender}}
	if c.r == nil {
		c.r = System
	}
	c.spf()
	c.dkim()
	c.dmarc()
	c.mx()
	c.mtaSTS()
	c.tlsRPT()
	c.bimi()
	sort.SliceStable(c.rep.Issues, func(i, j int) bool { return c.rep.Issues[i].Level.Rank() < c.rep.Issues[j].Level.Rank() })
	if c.rep.Issues == nil {
		c.rep.Issues = []model.Issue{}
	}
	return c.rep
}

type checker struct {
	ctx context.Context
	r   Resolver
	o   Options
	rep Report
}

func (c *checker) issue(check string, level model.Level, title, detail, fix string) {
	i := model.Issue{Check: "dns/" + check, Level: level, Title: title, Detail: detail, Fix: fix}
	i.Identify()
	c.rep.Issues = append(c.rep.Issues, i)
}

// txt returns the TXT records at name that start with prefix (a version
// tag such as "v=spf1"), compared case-insensitively. A missing name is
// not an error.
func (c *checker) txt(name, prefix string) ([]string, error) {
	recs, err := c.r.LookupTXT(c.ctx, name)
	if err != nil {
		if NotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return withPrefix(recs, prefix), nil
}

func withPrefix(recs []string, prefix string) []string {
	var out []string
	for _, r := range recs {
		t := strings.TrimSpace(r)
		if len(t) >= len(prefix) && strings.EqualFold(t[:len(prefix)], prefix) &&
			(len(t) == len(prefix) || t[len(prefix)] == ' ' || t[len(prefix)] == ';' || t[len(prefix)] == '\t') {
			out = append(out, t)
		}
	}
	return out
}

// NotFound reports whether err means the name or record doesn't exist
// (NXDOMAIN or no data), as opposed to a failed lookup.
func NotFound(err error) bool {
	var de *net.DNSError
	return errors.As(err, &de) && de.IsNotFound
}

// OrgDomain returns the organizational domain (the registrable domain under
// the public suffix list) that DMARC aligns on: mail.shop.example.co.uk →
// example.co.uk.
func OrgDomain(d string) string {
	d = normalize(d)
	if org, err := publicsuffix.EffectiveTLDPlusOne(d); err == nil {
		return org
	}
	return d
}

// Aligned reports whether domain a aligns with b under DMARC: the same
// organizational domain (relaxed) or the same name (strict).
func Aligned(a, b string, strict bool) bool {
	a, b = normalize(a), normalize(b)
	if a == "" || b == "" {
		return false
	}
	if strict {
		return a == b
	}
	return OrgDomain(a) == OrgDomain(b)
}

func normalize(d string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(d)), ".")
}

// tags parses "k=v; k=v" records (DKIM, DMARC, MTA-STS TXT, TLS-RPT,
// BIMI). Keys are lowercased; whitespace around keys and values is
// dropped. ok is false when a part has no "=".
func tags(rec string) (map[string]string, bool) {
	out := map[string]string{}
	ok := true
	for _, part := range strings.Split(rec, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, found := strings.Cut(part, "=")
		if !found {
			ok = false
			continue
		}
		out[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}
	return out, ok
}
