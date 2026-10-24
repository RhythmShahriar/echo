package dns

import (
	"fmt"
	"net"
	"regexp"
	"strings"

	"github.com/RhythmShahriar/echo/model"
)

// RFC 7208 limits: terms that cost a DNS query, and queries that may
// come back empty, before receivers give up with permerror.
const (
	maxLookups = 10
	maxVoids   = 2
	maxDepth   = 12 // include nesting before we call it a loop
)

// SPF is the domain's SPF record and what evaluating it costs.
type SPF struct {
	Domain   string   `json:"domain"`
	Record   string   `json:"record"`
	Lookups  int      `json:"lookups"`
	Voids    int      `json:"voids"`
	Includes []string `json:"includes,omitempty"` // every include and redirect, flattened
	All      string   `json:"all,omitempty"`      // the final all term: -all, ~all, ?all, +all
}

// spfWalk evaluates a record tree without a client IP: it counts lookups,
// collects includes and authorized networks, and notes problems.
type spfWalk struct {
	c        *checker
	lookups  int
	voids    int
	includes []string
	nets     []*net.IPNet
	problems []spfProblem
	visited  map[string]bool
}

type spfProblem struct {
	kind   string // syntax, missing-include, multiple, loop, lookup, ptr
	domain string
	detail string
}

var modifierName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]*=`)

func (c *checker) spf() {
	d := c.rep.Domain
	recs, err := c.txt(d, "v=spf1")
	if err != nil {
		c.issue("spf", model.Warning, "SPF lookup failed", fmt.Sprintf("TXT %s: %v", d, err),
			"Check the domain's nameservers answer TXT queries, then run the check again.")
		return
	}
	switch len(recs) {
	case 0:
		c.issue("spf", model.Error, "No SPF record",
			d+" publishes no v=spf1 TXT record, so receivers can't tell which servers may send for it and many will junk or reject its mail.",
			"Publish a TXT record at "+d+` such as "v=spf1 `+c.suggestInclude()+`~all" listing the services that send for the domain.`)
		return
	case 1:
	default:
		c.issue("spf", model.Error, "Multiple SPF records",
			fmt.Sprintf("%s publishes %d v=spf1 records; receivers treat that as a permanent error and SPF fails for all mail:\n%s", d, len(recs), strings.Join(recs, "\n")),
			"Merge them into one TXT record at "+d+" (one v=spf1, every include, one all at the end) and delete the others.")
		return
	}
	w := &spfWalk{c: c, visited: map[string]bool{d: true}}
	all := w.record(d, recs[0], 0)
	c.rep.SPF = &SPF{Domain: d, Record: recs[0], Lookups: w.lookups, Voids: w.voids, Includes: w.includes, All: all}
	w.report(d)

	switch all {
	case "+all":
		c.issue("spf", model.Error, "SPF allows every server (+all)",
			"The record ends in +all, so any server on the internet passes SPF for "+d+": SPF protects nothing and receivers distrust it.",
			"Change +all to ~all (or -all once every sender is listed) in the TXT record at "+d+".")
	case "?all":
		c.issue("spf", model.Warning, "SPF is neutral for every other server (?all)",
			"The record ends in ?all, which says nothing about servers it doesn't list, so spoofed mail isn't penalized.",
			"Change ?all to ~all (or -all once every sender is listed) in the TXT record at "+d+".")
	case "":
		c.issue("spf", model.Warning, "SPF record has no all mechanism",
			"Without an all term, unlisted servers get a neutral result, as with ?all.",
			"End the TXT record at "+d+" with ~all (or -all).")
	}
	if w.lookups > maxLookups {
		c.issue("spf", model.Error, "SPF needs more than 10 DNS lookups",
			fmt.Sprintf("Evaluating the record takes %d DNS lookups (include, a, mx, ptr, exists and redirect, counted through every include). Receivers stop at 10 and SPF fails with permerror. Includes: %s.",
				w.lookups, strings.Join(w.includes, ", ")),
			"Remove includes for services that no longer send for "+d+", replace a and mx with ip4:/ip6: ranges, or move a sending service to its own subdomain (its Return-Path domain) with a separate SPF record.")
	}
	if w.voids > maxVoids {
		c.issue("spf", model.Error, "SPF has more than 2 void lookups",
			fmt.Sprintf("%d lookups in the record return no answer; receivers fail SPF with permerror after 2.", w.voids),
			"Remove the include, a, mx or exists terms that point at names without records from the TXT record at "+d+".")
	}
	c.spfSender(d, w)
}

// suggestInclude names the known sender's include for a fix, or nothing.
func (c *checker) suggestInclude() string {
	if s := c.o.Sender; s != nil {
		if p, ok := ProviderByName(s.Provider); ok && len(p.SPF) > 0 {
			return "include:" + p.SPF[0] + " "
		}
	}
	return ""
}

// spfSender checks the record authorizes the service that actually sends.
// SPF is evaluated against the Return-Path domain, so when that's a
// subdomain of ours (mg.example.com, send.example.com, em123.example.com)
// its record is the one that matters. When it's the provider's own domain
// (bounces.sendgrid.net) the provider handles SPF and DMARC must align on
// DKIM instead; that's reported by the DMARC check.
func (c *checker) spfSender(d string, w *spfWalk) {
	s := c.o.Sender
	if s == nil || (s.Provider == "" && s.SMTPHost == "") {
		return
	}
	target := d
	if rp := normalize(s.ReturnPathDomain); rp != "" && rp != d {
		if OrgDomain(rp) != c.rep.OrgDomain {
			return
		}
		recs, err := c.txt(rp, "v=spf1")
		if err != nil {
			return
		}
		if len(recs) == 0 {
			c.issue("spf", model.Warning, "Bounce domain has no SPF record",
				"Mail is sent with Return-Path domain "+rp+", which SPF checks, but it publishes no v=spf1 record.",
				"Publish the TXT record your sending service gives for "+rp+` (typically "v=spf1 `+c.suggestInclude()+`~all").`)
			return
		}
		target = rp
		w = &spfWalk{c: c, visited: map[string]bool{rp: true}}
		w.record(rp, recs[0], 0)
	}
	if p, ok := ProviderByName(s.Provider); ok && len(p.SPF) > 0 {
		for _, inc := range w.includes {
			for _, want := range p.SPF {
				if OrgDomain(inc) == OrgDomain(want) {
					return
				}
			}
		}
	}
	if ip := c.senderIP(s.SMTPHost); ip != nil {
		for _, n := range w.nets {
			if n.Contains(ip) {
				return
			}
		}
	}
	who, fix := s.Provider, ""
	if p, ok := ProviderByName(s.Provider); ok {
		who = p.Label
		if len(p.SPF) > 0 {
			fix = "Add include:" + p.SPF[0] + " before the all term of the TXT record at " + target + "."
		}
	}
	if fix == "" && s.SMTPHost != "" {
		who = strings.TrimSpace(who + " " + s.SMTPHost)
		if ip := c.senderIP(s.SMTPHost); ip != nil {
			kind := "ip4"
			if ip.To4() == nil {
				kind = "ip6"
			}
			fix = "Add " + kind + ":" + ip.String() + " before the all term of the TXT record at " + target + "."
		} else {
			return // the relay doesn't resolve: nothing to compare against
		}
	}
	if who == "" || fix == "" {
		return
	}
	c.issue("spf", model.Error, "SPF does not authorize the sending service",
		"Mail is sent through "+who+", but the SPF record at "+target+" doesn't include it, so SPF fails for that mail.", fix)
}

func (c *checker) senderIP(host string) net.IP {
	if host == "" {
		return nil
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip
	}
	addrs, err := c.r.LookupHost(c.ctx, host)
	if err != nil || len(addrs) == 0 {
		return nil
	}
	return net.ParseIP(addrs[0])
}

// record walks one SPF record and returns its effective all term (from a
// redirect when the record has none).
func (w *spfWalk) record(domain, rec string, depth int) string {
	fields := strings.Fields(rec)
	all, redirect := "", ""
	for _, t := range fields[1:] {
		if modifierName.MatchString(t) {
			name, val, _ := strings.Cut(t, "=")
			switch strings.ToLower(name) {
			case "redirect":
				if redirect != "" {
					w.problem("syntax", domain, "more than one redirect= modifier")
				}
				redirect = val
			case "exp":
			default: // unknown modifiers are allowed and ignored
			}
			continue
		}
		qual := "+"
		if strings.ContainsAny(t[:1], "+-~?") {
			qual, t = t[:1], t[1:]
		}
		name, arg := t, ""
		if i := strings.IndexAny(t, ":/"); i >= 0 {
			name, arg = t[:i], t[i:]
		}
		arg = strings.TrimPrefix(arg, ":")
		switch strings.ToLower(name) {
		case "all":
			if arg != "" {
				w.problem("syntax", domain, "all takes no argument: "+t)
			}
			if all == "" {
				all = qual + "all"
			}
		case "include":
			if arg == "" || strings.HasPrefix(arg, "/") {
				w.problem("syntax", domain, "include needs a domain: "+t)
				continue
			}
			w.lookups++
			w.include(domain, arg, depth)
		case "a", "mx":
			w.lookups++
			target, cidr4, cidr6, ok := splitCIDR(arg, domain)
			if !ok {
				w.problem("syntax", domain, "bad "+name+" mechanism: "+t)
				continue
			}
			w.hosts(strings.ToLower(name), target, cidr4, cidr6)
		case "ptr":
			w.lookups++
			w.problem("ptr", domain, t)
		case "exists":
			if arg == "" {
				w.problem("syntax", domain, "exists needs a domain: "+t)
				continue
			}
			w.lookups++
		case "ip4", "ip6":
			n, ok := parseNet(arg, name == "ip4")
			if !ok {
				w.problem("syntax", domain, "bad "+name+" address: "+t)
				continue
			}
			w.nets = append(w.nets, n)
		default:
			w.problem("syntax", domain, "unknown mechanism: "+t)
		}
	}
	if redirect != "" && all == "" {
		w.lookups++
		if r := w.include(domain, redirect, depth); r != "" {
			all = r
		}
	}
	return all
}

// include follows an include or redirect target and returns its all term.
func (w *spfWalk) include(from, target string, depth int) string {
	target = normalize(target)
	if strings.Contains(target, "%") {
		return "" // macro: depends on the sender, can't be followed offline
	}
	w.includes = append(w.includes, target)
	if depth >= maxDepth || w.visited[target] {
		w.problem("loop", from, "include loop through "+target)
		return ""
	}
	w.visited[target] = true
	defer delete(w.visited, target)
	recs, err := w.c.txt(target, "v=spf1")
	if err != nil {
		w.problem("lookup", target, err.Error())
		return ""
	}
	switch len(recs) {
	case 0:
		w.voids++
		w.problem("missing-include", from, target)
		return ""
	case 1:
	default:
		w.problem("multiple", target, fmt.Sprintf("%d v=spf1 records", len(recs)))
		return ""
	}
	return w.record(target, recs[0], depth+1)
}

// hosts resolves an a or mx mechanism into authorized networks. MX host
// address lookups don't count toward the limit of 10 (RFC 7208 4.6.4).
func (w *spfWalk) hosts(kind, target string, cidr4, cidr6 int) {
	if strings.Contains(target, "%") {
		return
	}
	names := []string{target}
	if kind == "mx" {
		mxs, err := w.c.r.LookupMX(w.c.ctx, target)
		if err != nil && !NotFound(err) {
			w.problem("lookup", target, err.Error())
			return
		}
		if len(mxs) == 0 {
			w.voids++
			return
		}
		names = names[:0]
		for _, m := range mxs {
			names = append(names, normalize(m.Host))
		}
	}
	for _, n := range names {
		addrs, err := w.c.r.LookupHost(w.c.ctx, n)
		if err != nil {
			if NotFound(err) && kind == "a" {
				w.voids++
			}
			continue
		}
		for _, a := range addrs {
			ip := net.ParseIP(a)
			if ip == nil {
				continue
			}
			bits, size := cidr4, 32
			if ip.To4() == nil {
				bits, size = cidr6, 128
			} else {
				ip = ip.To4()
			}
			w.nets = append(w.nets, &net.IPNet{IP: ip.Mask(net.CIDRMask(bits, size)), Mask: net.CIDRMask(bits, size)})
		}
	}
}

func (w *spfWalk) problem(kind, domain, detail string) {
	w.problems = append(w.problems, spfProblem{kind, domain, detail})
}

// report turns walk problems into issues, one per kind.
func (w *spfWalk) report(d string) {
	by := map[string][]string{}
	var order []string
	for _, p := range w.problems {
		if _, ok := by[p.kind]; !ok {
			order = append(order, p.kind)
		}
		by[p.kind] = append(by[p.kind], p.domain+": "+p.detail)
	}
	c := w.c
	for _, k := range order {
		detail := strings.Join(by[k], "\n")
		switch k {
		case "syntax":
			c.issue("spf", model.Error, "SPF record has a syntax error",
				"Receivers fail SPF with permerror on a malformed record:\n"+detail,
				"Fix the listed terms in the TXT record at "+d+" (or in the included domain's record, if it's yours).")
		case "missing-include":
			c.issue("spf", model.Error, "SPF includes a domain without an SPF record",
				"An include must point at a domain with its own v=spf1 record, otherwise SPF fails with permerror:\n"+detail,
				"Remove the include from the TXT record at "+d+", or correct it to the domain your provider documents.")
		case "multiple":
			c.issue("spf", model.Error, "SPF includes a domain with multiple SPF records",
				"An included domain publishes more than one v=spf1 record, which is a permanent error:\n"+detail,
				"Remove or replace the include in the TXT record at "+d+"; ask the included domain's owner to merge its records.")
		case "loop":
			c.issue("spf", model.Error, "SPF includes loop",
				"The includes refer back to themselves:\n"+detail,
				"Remove the include that points back up the chain from the TXT record at "+d+".")
		case "lookup":
			c.issue("spf", model.Warning, "SPF include lookup failed",
				"A lookup failed while following the record, so receivers may get temperror:\n"+detail,
				"Check the listed domain's nameservers answer TXT queries.")
		case "ptr":
			c.issue("spf", model.Notice, "SPF uses the ptr mechanism",
				"ptr is slow, unreliable and deprecated (RFC 7208 5.5); some receivers ignore it:\n"+detail,
				"Replace ptr with ip4:/ip6: ranges or an include in the TXT record at "+d+".")
		}
	}
}

// splitCIDR parses an a or mx argument: [domain][/cidr4][//cidr6].
func splitCIDR(arg, def string) (target string, c4, c6 int, ok bool) {
	c4, c6 = 32, 128
	target = arg
	if i := strings.Index(arg, "/"); i >= 0 {
		target = arg[:i]
		rest := arg[i:]
		if v6, found := strings.CutPrefix(rest, "//"); found {
			if _, err := fmt.Sscanf(v6, "%d", &c6); err != nil || c6 < 0 || c6 > 128 {
				return "", 0, 0, false
			}
		} else {
			v4, v6s, _ := strings.Cut(strings.TrimPrefix(rest, "/"), "//")
			if _, err := fmt.Sscanf(v4, "%d", &c4); err != nil || c4 < 0 || c4 > 32 {
				return "", 0, 0, false
			}
			if v6s != "" {
				if _, err := fmt.Sscanf(v6s, "%d", &c6); err != nil || c6 < 0 || c6 > 128 {
					return "", 0, 0, false
				}
			}
		}
	}
	if target == "" {
		target = def
	}
	return normalize(target), c4, c6, true
}

func parseNet(arg string, v4 bool) (*net.IPNet, bool) {
	if !strings.Contains(arg, "/") {
		if v4 {
			arg += "/32"
		} else {
			arg += "/128"
		}
	}
	ip, n, err := net.ParseCIDR(arg)
	if err != nil || (ip.To4() != nil) != v4 {
		return nil, false
	}
	return n, true
}
