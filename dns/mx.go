// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package dns

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/model"
)

// MTASTS is the domain's MTA-STS record and, when fetched, its policy.
type MTASTS struct {
	Record string   `json:"record"`
	ID     string   `json:"id,omitempty"`
	URL    string   `json:"url,omitempty"` // set when the policy was fetched
	Mode   string   `json:"mode,omitempty"`
	MX     []string `json:"mx,omitempty"`
	MaxAge string   `json:"max_age,omitempty"`
}

const maxPolicy = 64 << 10 // RFC 8461 policies are tiny; cap what we read

func (c *checker) mx() {
	d := c.rep.Domain
	mxs, err := c.r.LookupMX(c.ctx, d)
	if err != nil && !NotFound(err) {
		c.issue("mx", model.Warning, "MX lookup failed", fmt.Sprintf("MX %s: %v", d, err),
			"Check the domain's nameservers answer MX queries, then run the check again.")
		return
	}
	if len(mxs) == 0 {
		c.issue("mx", model.Warning, "No MX record",
			d+" has no MX record, so replies and bounces to it can't be delivered. Some receivers also reject mail whose From domain can't receive mail.",
			"Publish an MX record at "+d+" pointing at the mailbox provider that receives its mail.")
		return
	}
	sort.SliceStable(mxs, func(i, j int) bool { return mxs[i].Pref < mxs[j].Pref })
	if len(mxs) == 1 && (mxs[0].Host == "." || mxs[0].Host == "") {
		c.rep.NullMX = true
		level, more := model.Notice, ""
		if c.o.Sender != nil {
			level, more = model.Warning, " Yet mail is sent from it: receivers may reject mail whose From or Return-Path domain declares it accepts none (RFC 7505)."
		}
		c.issue("mx", level, "Domain has a null MX",
			d+` publishes "MX 0 .", saying it accepts no mail.`+more,
			"If "+d+" sends mail, replace the null MX with an MX record for the mailbox that receives replies, or send from a domain that receives mail.")
		return
	}
	var dead []string
	for _, m := range mxs {
		h := normalize(m.Host)
		c.rep.MX = append(c.rep.MX, h)
		if _, err := c.r.LookupHost(c.ctx, h); err != nil && NotFound(err) {
			dead = append(dead, h)
		}
	}
	if len(dead) > 0 {
		c.issue("mx", model.Warning, "MX host does not resolve",
			"These MX hosts have no A or AAAA record, so senders can't reach them: "+strings.Join(dead, ", ")+".",
			"Correct or remove the MX records at "+d+" that point at "+strings.Join(dead, ", ")+".")
	}
}

func (c *checker) mtaSTS() {
	d := c.rep.Domain
	recs, err := c.txt("_mta-sts."+d, "v=STSv1")
	if err != nil {
		return
	}
	if len(recs) == 0 {
		if len(c.rep.MX) > 0 {
			c.issue("mta-sts", model.Notice, "No MTA-STS policy",
				"Without MTA-STS, servers sending to "+d+" fall back to plain text if TLS is stripped in transit.",
				`Publish a TXT record at _mta-sts.`+d+` ("v=STSv1; id=<serial>") and a policy file at https://mta-sts.`+d+`/.well-known/mta-sts.txt.`)
		}
		return
	}
	if len(recs) > 1 {
		c.issue("mta-sts", model.Warning, "Multiple MTA-STS records",
			"_mta-sts."+d+" has more than one v=STSv1 record; senders ignore MTA-STS when that happens.",
			"Keep one TXT record at _mta-sts."+d+".")
		return
	}
	t, _ := tags(recs[0])
	m := &MTASTS{Record: recs[0], ID: t["id"]}
	c.rep.MTASTS = m
	if m.ID == "" {
		c.issue("mta-sts", model.Warning, "MTA-STS record has no id",
			"_mta-sts."+d+" lacks id=, which senders need to notice policy changes; they ignore the record.",
			`Add id=<date or serial> to the TXT record at _mta-sts.`+d+`, changing it whenever the policy file changes.`)
	}
	if c.o.HTTP != nil {
		c.mtaSTSPolicy(m)
	}
}

// mtaSTSPolicy fetches https://mta-sts.<domain>/.well-known/mta-sts.txt.
// The host is always the domain's own policy host and redirects aren't
// followed (RFC 8461 3.3), so this can't be steered elsewhere.
func (c *checker) mtaSTSPolicy(m *MTASTS) {
	d := c.rep.Domain
	u := "https://mta-sts." + d + "/.well-known/mta-sts.txt"
	m.URL = u
	fix := "Serve the policy over HTTPS with a valid certificate for mta-sts." + d + " at " + u + " (text/plain, no redirect)."
	body, err := c.fetch(u)
	if err != nil {
		c.issue("mta-sts", model.Error, "MTA-STS policy file cannot be fetched",
			"The TXT record announces MTA-STS but "+u+" failed: "+err.Error()+". Senders that cached an earlier policy may refuse to deliver.", fix)
		return
	}
	sc := bufio.NewScanner(strings.NewReader(body))
	version := ""
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "version":
			version = v
		case "mode":
			m.Mode = v
		case "mx":
			m.MX = append(m.MX, strings.ToLower(v))
		case "max_age":
			m.MaxAge = v
		}
	}
	if version != "STSv1" || (m.Mode != "enforce" && m.Mode != "testing" && m.Mode != "none") || m.MaxAge == "" {
		c.issue("mta-sts", model.Error, "MTA-STS policy file is invalid",
			"The policy at "+u+" needs version: STSv1, mode: enforce|testing|none, max_age and mx lines.", fix)
		return
	}
	if m.Mode == "testing" {
		c.issue("mta-sts", model.Notice, "MTA-STS is in testing mode",
			"Senders report TLS failures but still deliver over plain text.",
			"Change mode: testing to mode: enforce in "+u+" once TLS-RPT reports are clean and update the id in _mta-sts."+d+".")
	}
	if m.Mode == "none" {
		return
	}
	var uncovered []string
	for _, h := range c.rep.MX {
		if !mxCovered(h, m.MX) {
			uncovered = append(uncovered, h)
		}
	}
	if len(uncovered) > 0 {
		c.issue("mta-sts", model.Error, "MTA-STS policy does not cover the MX hosts",
			"In "+m.Mode+" mode, senders refuse to deliver to MX hosts the policy doesn't list: "+strings.Join(uncovered, ", ")+".",
			"Add mx: lines for "+strings.Join(uncovered, ", ")+" to "+u+" and change the id in _mta-sts."+d+".")
	}
}

func mxCovered(host string, patterns []string) bool {
	for _, p := range patterns {
		p = normalize(p)
		if p == host {
			return true
		}
		if rest, ok := strings.CutPrefix(p, "*."); ok {
			if strings.HasSuffix(host, "."+rest) && !strings.Contains(strings.TrimSuffix(host, "."+rest), ".") {
				return true
			}
		}
	}
	return false
}

func (c *checker) fetch(u string) (string, error) {
	hc := *c.o.HTTP
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if hc.Timeout == 0 {
		hc.Timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(c.ctx, hc.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	resp, err := hc.Do(req)
	if err != nil {
		var ue interface{ Unwrap() error }
		if errors.As(err, &ue) {
			return "", ue.Unwrap()
		}
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxPolicy))
	return string(b), err
}

func (c *checker) tlsRPT() {
	d := c.rep.Domain
	recs, err := c.txt("_smtp._tls."+d, "v=TLSRPTv1")
	if err != nil {
		return
	}
	if len(recs) == 0 {
		if c.rep.MTASTS != nil {
			c.issue("tls-rpt", model.Notice, "No TLS-RPT record",
				"MTA-STS is set up but nobody receives reports of TLS failures, so a broken certificate goes unnoticed.",
				`Publish a TXT record at _smtp._tls.`+d+` such as "v=TLSRPTv1; rua=mailto:tls-reports@`+c.rep.OrgDomain+`".`)
		}
		return
	}
	c.rep.TLSRPT = recs[0]
	if t, _ := tags(recs[0]); t["rua"] == "" {
		c.issue("tls-rpt", model.Warning, "TLS-RPT record has no rua",
			"_smtp._tls."+d+" has no rua=, so reports have nowhere to go.",
			"Add rua=mailto:tls-reports@"+c.rep.OrgDomain+" to the TXT record at _smtp._tls."+d+".")
	}
}

// bimi is optional, so it only ever produces notices.
func (c *checker) bimi() {
	d := c.rep.Domain
	recs, err := c.txt("default._bimi."+d, "v=BIMI1")
	if err != nil {
		return
	}
	enforced := c.rep.DMARC != nil && (c.rep.DMARC.Policy == "quarantine" || c.rep.DMARC.Policy == "reject") && c.rep.DMARC.Pct == 100
	if len(recs) == 0 {
		if enforced {
			c.issue("bimi", model.Notice, "No BIMI record",
				"DMARC is enforced, so "+d+" qualifies for BIMI: supporting inboxes can show its logo next to its mail.",
				`Optionally publish a TXT record at default._bimi.`+d+` ("v=BIMI1; l=https://`+d+`/logo.svg; a=<VMC URL>") with an SVG Tiny PS logo.`)
		}
		return
	}
	c.rep.BIMI = recs[0]
	if !enforced {
		c.issue("bimi", model.Notice, "BIMI needs DMARC enforcement",
			"default._bimi."+d+" exists but inboxes only show the logo when DMARC is p=quarantine or p=reject at pct=100.",
			"Raise the DMARC policy at _dmarc."+d+" to quarantine or reject.")
	}
}
