// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package sendmail

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/wire"
)

// target is where the shim hands a message: Echo's SMTP sink, or the
// capture API when there is no sink it can reach.
type target struct {
	smtp, api string
	from      string // where the address came from, for error messages
}

// findTarget works out where Echo is. PHP runs sendmail_path with a
// bare environment, so a sink on any port but 1025 would be missed
// without these fallbacks, in this order:
//
//  1. RHYTHM_ECHO_SMTP
//  2. RHYTHM_ECHO_URL, asking its /v1/health for the SMTP address (the
//     capture API when it reports none)
//  3. smtp: in the nearest .echo/config.yaml, from the working folder up
//     (the API at its listen: when smtp is off)
//  4. 127.0.0.1:1025
func findTarget(getenv func(string) string, stderr io.Writer) target {
	if addr := getenv(EnvSMTP); addr != "" {
		return target{smtp: addr, from: EnvSMTP}
	}
	if api := getenv(EnvURL); api != "" {
		if addr := healthSMTP(api, getenv(EnvToken)); addr != "" {
			return target{smtp: addr, from: EnvURL + "'s /v1/health"}
		}
		return target{api: api, from: EnvURL}
	}
	if path := nearestConfig(); path != "" {
		smtp, listen, err := readConfig(path)
		switch {
		case err != nil:
			fmt.Fprintf(stderr, "rhythm-echo sendmail: ignoring %s: %v\n", path, err)
		case smtp == "off":
			return target{api: "http://" + listen, from: path}
		default:
			return target{smtp: smtp, from: path}
		}
	}
	return target{smtp: DefaultSMTP, from: "the default"}
}

// healthSMTP asks a running Echo where its SMTP sink listens. It returns
// "" when Echo can't be asked, reports no sink, or the sink only listens
// on a loopback this machine can't reach.
func healthSMTP(base, token string) string {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(base, "/")+"/v1/health", nil) //nolint:gosec // the operator's own Echo
	if err != nil {
		return ""
	}
	if token != "" {
		req.Header.Set(wire.TokenHeader, token)
	}
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req) //nolint:gosec // the operator's own Echo
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	var h struct {
		Tool string `json:"tool"`
		SMTP string `json:"smtp"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&h) != nil ||
		h.Tool != "rhythm-echo" || h.SMTP == "" || h.SMTP == "off" {
		return ""
	}
	return reachable(h.SMTP, base)
}

// reachable turns the address Echo's sink listens on into one to dial
// from here, given the URL Echo's API is reached at. A sink on all
// interfaces is reached on the API's host; one on loopback only when the
// API is on loopback too, since Echo may be in a container or on
// another machine.
func reachable(listen, base string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return ""
	}
	u, err := url.Parse(base)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	apiHost := u.Hostname()
	ip := net.ParseIP(host)
	switch {
	case host == "" || (ip != nil && ip.IsUnspecified()):
		return net.JoinHostPort(apiHost, port)
	case isLoopback(host) && !isLoopback(apiHost):
		return ""
	}
	return listen
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// nearestConfig returns .echo/config.yaml in the working folder or the
// closest folder above it, or "".
func nearestConfig() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		p := filepath.Join(dir, config.Path())
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// readConfig reads only the addresses from an Echo config. It doesn't
// use config.Load: a typo in an action must not send mail elsewhere.
func readConfig(path string) (smtp, listen string, err error) {
	b, err := os.ReadFile(path) //nolint:gosec // the project's own config
	if err != nil {
		return "", "", err
	}
	var c struct {
		SMTP   string `yaml:"smtp"`
		Listen string `yaml:"listen"`
	}
	if err := yaml.Unmarshal(b, &c); err != nil {
		return "", "", err
	}
	d := config.Default()
	if c.SMTP == "" {
		c.SMTP = d.SMTP
	}
	if c.Listen == "" {
		c.Listen = d.Listen
	}
	return c.SMTP, c.Listen, nil
}
