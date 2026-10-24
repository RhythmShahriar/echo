// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/compare"
	"github.com/RhythmShahriar/echo/model"
)

// fromDomains lists the domains the emails in a run were sent from.
func fromDomains(r *model.Run) []string {
	var out []string
	for _, a := range r.Actions {
		for _, m := range a.Messages {
			if m.Channel != model.Email || m.From == "" {
				continue
			}
			addr := m.From
			if p, err := mail.ParseAddress(m.From); err == nil {
				addr = p.Address
			}
			if _, d, ok := strings.Cut(addr, "@"); ok && !slices.Contains(out, strings.ToLower(d)) {
				out = append(out, strings.ToLower(d))
			}
		}
	}
	slices.Sort(out)
	return out
}

// checkDomains runs `rhythm-echo check` on each domain against the fixture
// DNS, with what's known of the sending service and returns every issue
// it reported, labeled with its domain.
func checkDomains(ctx context.Context, e *Echo, timeout time.Duration, dir, state string, d *DNS, domains []string) ([]model.Issue, error) {
	var all []model.Issue
	for _, dom := range domains {
		args := []string{"check", dom, "--json", "--resolver", DNSAddr}
		if d.Provider != "" {
			args = append(args, "--provider", d.Provider)
		}
		if d.ReturnPath != "" {
			args = append(args, "--return-path", d.ReturnPath)
		}
		out, err := e.Run(ctx, timeout, dir, args...)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(dir, "check-"+state+"-"+dom+".json"), []byte(out), 0o600); err != nil {
			return nil, err
		}
		var rep struct {
			Issues []model.Issue `json:"issues"`
		}
		if err := json.Unmarshal([]byte(out), &rep); err != nil {
			return nil, fmt.Errorf("check %s: %w", dom, err)
		}
		for _, i := range rep.Issues {
			i.Detail = dom + ": " + i.Detail
			all = append(all, i)
		}
	}
	return all, nil
}

// checkRun runs the deliverability checks on the domains a run's emails
// were sent from. It must run while the scenario is in that run's state:
// the fixture DNS follows the state too.
func checkRun(ctx context.Context, e *Echo, timeout time.Duration, wd, state string, d *DNS, r *model.Run) ([]model.Issue, []string, error) {
	domains := fromDomains(r)
	if len(domains) == 0 {
		return nil, nil, errors.New("no email was sent, so there's no From domain to check")
	}
	issues, err := checkDomains(ctx, e, timeout, wd, state, d, domains)
	return issues, domains, err
}

// deliverability adds to cmp what following compare's advice for a new
// sender shows: the changes between `rhythm-echo check` on the domains the
// good run sent from and on those the broken run sends from, diffed with
// Echo's own issue comparison.
func deliverability(cmp *model.Comparison, good, broken []model.Issue, domains []string) {
	for _, c := range compare.Issues(good, broken) {
		c.Actions = []string{"rhythm-echo check " + strings.Join(domains, ", ")}
		cmp.Changes = append(cmp.Changes, c)
	}
	compare.Sort(cmp.Changes)
}

// under resolves a folder from Echo's output, relative to where it ran.
func under(wd, dir string) string {
	if filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(wd, dir)
}
