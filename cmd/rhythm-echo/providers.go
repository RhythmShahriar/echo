// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/recognize"
)

// providerEntry is one provider in providers --json.
type providerEntry struct {
	Provider string        `json:"provider"`
	Name     string        `json:"name"`
	Channel  model.Channel `json:"channel"`
	Hosts    []string      `json:"hosts"` // empty: any host, told by its headers
	YAML     bool          `json:"yaml"`  // described in recognize/providers rather than Go
	Payment  string        `json:"payment,omitempty"`
}

// cmdProviders lists the services Echo recognizes, by channel: what it
// files their messages under, the hosts it knows them by and, for a
// payment gateway, that its live calls are always refused.
func cmdProviders(s *session) int {
	if len(s.args) > 0 {
		return s.fail(usageErr("providers takes no arguments"))
	}
	gateways := map[string]bool{}
	for _, g := range recognize.PaymentGateways() {
		gateways[g.Provider] = true
	}
	var entries []providerEntry
	for _, e := range recognize.Catalog() {
		p := providerEntry{Provider: e.Provider, Name: e.Name, Channel: e.Channel, Hosts: nonNil(e.Hosts), YAML: e.Declared}
		if gateways[e.Provider] {
			p.Payment = "live calls refused"
		}
		entries = append(entries, p)
	}
	if s.o.json {
		return jsonOut(s, kindProviders, entries)
	}
	tw := tabwriter.NewWriter(s.stdout, 0, 0, 2, ' ', 0)
	var channel model.Channel
	for i, e := range entries {
		if e.Channel != channel {
			if i > 0 {
				fmt.Fprintln(tw)
			}
			channel = e.Channel
			fmt.Fprintf(tw, "%s (%d)\n", strings.ToUpper(string(channel)), countChannel(entries, channel))
		}
		hosts := strings.Join(e.Hosts, ", ")
		if hosts == "" {
			hosts = "any host, by its headers"
		}
		var notes []string
		if e.YAML {
			notes = append(notes, "YAML")
		}
		if e.Payment != "" {
			notes = append(notes, e.Payment)
		}
		note := ""
		if len(notes) > 0 {
			note = " (" + strings.Join(notes, "; ") + ")"
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s%s\n", e.Name, e.Provider, hosts, note)
	}
	_ = tw.Flush()
	if !s.o.quiet {
		fmt.Fprintf(s.stdout, "\n%s recognized. Anything else is captured as a generic webhook or request.\n", plural(len(entries), "service"))
	}
	return exitOK
}

func countChannel(entries []providerEntry, c model.Channel) int {
	n := 0
	for _, e := range entries {
		if e.Channel == c {
			n++
		}
	}
	return n
}
