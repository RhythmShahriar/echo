package dns

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/RhythmShahriar/echo/model"
)

// DMARC is the domain's DMARC policy.
type DMARC struct {
	Name      string   `json:"name"` // where it was found: _dmarc.domain or _dmarc.orgdomain
	Record    string   `json:"record"`
	Inherited bool     `json:"inherited,omitempty"` // from the organizational domain
	Policy    string   `json:"policy"`              // the one that applies to this domain
	P         string   `json:"p"`
	SP        string   `json:"sp,omitempty"`
	Pct       int      `json:"pct"`
	ADKIM     string   `json:"adkim"`
	ASPF      string   `json:"aspf"`
	RUA       []string `json:"rua,omitempty"`
	RUF       []string `json:"ruf,omitempty"`
}

func (c *checker) dmarc() {
	d, org := c.rep.Domain, c.rep.OrgDomain
	name := "_dmarc." + d
	recs, err := c.txt(name, "v=DMARC1")
	if err != nil {
		c.issue("dmarc", model.Warning, "DMARC lookup failed", fmt.Sprintf("TXT %s: %v", name, err),
			"Check the domain's nameservers answer TXT queries, then run the check again.")
		return
	}
	inherited := false
	if len(recs) == 0 && org != d {
		name = "_dmarc." + org
		if recs, err = c.txt(name, "v=DMARC1"); err != nil {
			recs = nil
		}
		inherited = len(recs) > 0
	}
	switch len(recs) {
	case 0:
		c.issue("dmarc", model.Error, "No DMARC record",
			"Neither _dmarc."+d+" nor the organizational domain publishes a DMARC policy. Gmail, Yahoo and Microsoft require one from bulk senders, and without it spoofed mail from "+d+" is judged on SPF and DKIM alone.",
			`Publish a TXT record at _dmarc.`+d+` such as "v=DMARC1; p=none; rua=mailto:dmarc@`+org+`", read the reports, then raise p to quarantine.`)
		return
	case 1:
	default:
		c.issue("dmarc", model.Error, "Multiple DMARC records",
			fmt.Sprintf("%s publishes %d v=DMARC1 records; receivers then ignore DMARC entirely.", name, len(recs)),
			"Keep one TXT record at "+name+" and delete the others.")
		return
	}
	t, ok := tags(recs[0])
	m := &DMARC{Name: name, Record: recs[0], Inherited: inherited, P: strings.ToLower(t["p"]), SP: strings.ToLower(t["sp"]),
		Pct: 100, ADKIM: "r", ASPF: "r"}
	c.rep.DMARC = m
	var bad []string
	if !ok {
		bad = append(bad, "a part without = ")
	}
	if !validPolicy(m.P) {
		bad = append(bad, fmt.Sprintf("p=%q (must be none, quarantine or reject)", t["p"]))
	}
	if _, has := t["sp"]; has && !validPolicy(m.SP) {
		bad = append(bad, fmt.Sprintf("sp=%q", t["sp"]))
	}
	if v, has := t["pct"]; has {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 100 {
			bad = append(bad, fmt.Sprintf("pct=%q (must be 0-100)", v))
		} else {
			m.Pct = n
		}
	}
	for _, k := range []string{"adkim", "aspf"} {
		if v, has := t[k]; has {
			v = strings.ToLower(v)
			if v != "r" && v != "s" {
				bad = append(bad, fmt.Sprintf("%s=%q (must be r or s)", k, v))
				continue
			}
			if k == "adkim" {
				m.ADKIM = v
			} else {
				m.ASPF = v
			}
		}
	}
	m.RUA = splitURIs(t["rua"])
	m.RUF = splitURIs(t["ruf"])
	if len(bad) > 0 {
		c.issue("dmarc", model.Error, "DMARC record is invalid",
			name+" has "+strings.Join(bad, ", ")+". Receivers discard an invalid record and treat the domain as having no DMARC policy.",
			`Republish the TXT record at `+name+` in the form "v=DMARC1; p=quarantine; rua=mailto:dmarc@`+org+`".`)
		if !validPolicy(m.P) {
			return
		}
	}
	m.Policy = m.P
	if inherited && m.SP != "" {
		m.Policy = m.SP
	}

	switch m.Policy {
	case "none":
		c.issue("dmarc", model.Notice, "DMARC policy is none",
			"p=none is monitoring only: receivers send reports but do nothing to mail that fails DMARC, so spoofing "+d+" still works. It meets the Gmail and Yahoo bulk-sender minimum. Move on once the reports show all legitimate mail passes.",
			"After a few weeks of clean aggregate reports, change the TXT record at "+name+" to p=quarantine, then p=reject.")
	}
	if len(m.RUA) == 0 {
		c.issue("dmarc", model.Warning, "DMARC sends no aggregate reports",
			name+" has no rua= tag, so nobody learns which servers send as "+d+" or what fails.",
			"Add rua=mailto:dmarc@"+org+" (or a DMARC report service's address) to the TXT record at "+name+".")
	}
	if m.Pct < 100 && m.Policy != "none" {
		c.issue("dmarc", model.Notice, "DMARC policy applies to only part of the mail",
			fmt.Sprintf("pct=%d: the %s policy applies to %d%% of failing mail; the rest is treated as p=none.", m.Pct, m.Policy, m.Pct),
			"Raise pct to 100 (or remove the tag) in the TXT record at "+name+" once reports are clean.")
	}
	if !inherited && d == org && m.SP == "none" && m.P != "none" {
		c.issue("dmarc", model.Notice, "DMARC leaves subdomains unprotected",
			"sp=none: spoofed mail from any subdomain of "+d+" passes, even though the domain itself is protected by p="+m.P+".",
			"Remove sp=none (subdomains then inherit p) from the TXT record at "+name+", or publish separate policies for subdomains that send.")
	}
	c.dmarcReports(m)
	c.dmarcAlignment(m)
}

func validPolicy(p string) bool { return p == "none" || p == "quarantine" || p == "reject" }

func splitURIs(v string) []string {
	var out []string
	for _, u := range strings.Split(v, ",") {
		if u = strings.TrimSpace(u); u != "" {
			out = append(out, u)
		}
	}
	return out
}

// dmarcReports checks that report addresses outside the domain have agreed
// to receive its reports (RFC 7489 7.1): a TXT record at
// <domain>._report._dmarc.<report domain>. Receivers drop reports otherwise.
func (c *checker) dmarcReports(m *DMARC) {
	for _, uri := range m.RUA {
		u, err := url.Parse(uri)
		if err != nil || !strings.EqualFold(u.Scheme, "mailto") {
			continue
		}
		addr := u.Opaque
		if i := strings.IndexAny(addr, "!?"); i >= 0 {
			addr = addr[:i]
		}
		_, host, found := strings.Cut(addr, "@")
		if !found || OrgDomain(host) == c.rep.OrgDomain {
			continue
		}
		name := c.rep.OrgDomain + "._report._dmarc." + normalize(host)
		recs, err := c.txt(name, "v=DMARC1")
		if err != nil || len(recs) > 0 {
			continue
		}
		c.issue("dmarc", model.Warning, "DMARC report address hasn't authorized reports",
			"rua points at "+addr+", outside "+c.rep.OrgDomain+", but "+name+" has no v=DMARC1 record, so receivers won't send it reports.",
			`Ask the report service to publish TXT "v=DMARC1" at `+name+`, or use an address at `+c.rep.OrgDomain+`.`)
	}
}

// dmarcAlignment checks the known sender's mail would pass DMARC: its DKIM
// d= or its Return-Path domain must align with the From domain (here, the
// checked domain).
func (c *checker) dmarcAlignment(m *DMARC) {
	s := c.o.Sender
	if s == nil || (s.DKIMDomain == "" && s.ReturnPathDomain == "") {
		return
	}
	from := c.rep.Domain
	dkimR, dkimS := Aligned(s.DKIMDomain, from, false), Aligned(s.DKIMDomain, from, true)
	spfR, spfS := Aligned(s.ReturnPathDomain, from, false), Aligned(s.ReturnPathDomain, from, true)
	dkimOK := dkimR && (m.ADKIM == "r" || dkimS)
	spfOK := spfR && (m.ASPF == "r" || spfS)
	who := s.Provider
	if p, ok := ProviderByName(s.Provider); ok {
		who = p.Label
	}
	if who == "" {
		who = "the sending service"
	}
	detail := fmt.Sprintf("From domain %s; DKIM d=%s; Return-Path domain %s (adkim=%s, aspf=%s).",
		from, orDash(s.DKIMDomain), orDash(s.ReturnPathDomain), m.ADKIM, m.ASPF)
	switch {
	case !dkimOK && !spfOK && (dkimR || spfR):
		c.issue("dmarc", model.Error, "DMARC strict alignment fails for the sending service",
			"Mail from "+who+" aligns only in relaxed mode, but the policy asks for strict (s). "+detail,
			"Sign with d="+from+" and send with a Return-Path at "+from+" exactly, or set adkim=r and aspf=r in the TXT record at "+m.Name+".")
	case !dkimOK && !spfOK:
		c.issue("dmarc", model.Error, "Mail from the sending service fails DMARC alignment",
			"Neither the DKIM signature nor the Return-Path of mail from "+who+" uses "+from+", so DMARC fails even when SPF and DKIM pass. "+detail,
			"Set up domain authentication in "+who+": publish its DKIM records under "+from+" so it signs with d="+from+", and a custom Return-Path (bounce) subdomain.")
	case !dkimOK && spfOK:
		c.issue("dmarc", model.Notice, "DMARC passes on SPF alone",
			"Mail from "+who+" aligns through its Return-Path only; forwarding breaks SPF, so forwarded mail will fail DMARC. "+detail,
			"Have "+who+" sign with d="+from+" by publishing its DKIM records under "+from+".")
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
