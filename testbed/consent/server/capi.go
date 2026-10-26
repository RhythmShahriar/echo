// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// capiScenario is the scenario whose shop sends a Meta Conversions API
// event from the server (c18).
const capiScenario = "c18"

// CAPIPixel is the fake pixel ID c18's server-side events name.
const CAPIPixel = "100000000000001"

// capiSender sends c18's server-side events through Echo's proxy only.
// Its dialer can reach nothing but the proxy, so even a wrong URL can't
// leave the machine.
type capiSender struct {
	client *http.Client
}

// newCAPISender returns a sender through proxy, which must be an http
// URL on a loopback address or a single-label name (a Compose service
// such as echo), trusting the CA in caFile.
func newCAPISender(proxy, caFile string) (*capiSender, error) {
	u, err := url.Parse(proxy)
	if err != nil || u.Scheme != "http" || u.Host == "" {
		return nil, fmt.Errorf("-echo-proxy %q: want http://host:port of rhythm-echo up", proxy)
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); (ip == nil && strings.Contains(host, ".")) || (ip != nil && !ip.IsLoopback()) {
		return nil, fmt.Errorf("-echo-proxy %q: want a loopback address or a Compose service name", proxy)
	}
	tr := &http.Transport{
		Proxy: http.ProxyURL(u),
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if addr != u.Host {
				return nil, errors.New("consent testbed: only Echo's proxy may be dialed")
			}
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		},
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}
	if caFile != "" {
		pem, err := os.ReadFile(caFile) //nolint:gosec // Echo's CA, named on the command line
		if err != nil {
			return nil, fmt.Errorf("-echo-ca: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("-echo-ca %s: no certificate in it", caFile)
		}
		tr.TLSClientConfig.RootCAs = pool
	}
	return &capiSender{client: &http.Client{Transport: tr, Timeout: 10 * time.Second}}, nil
}

// maybeSend sends a PageView event for the page in r: in the good state
// only when the visitor accepted marketing (Complianz's cmplz_marketing
// cookie says allow), in the broken state always, which is the bug c18
// models. The event carries r's X-Echo-Run, so Echo links it to the run.
func (c *capiSender) maybeSend(r *http.Request, broken bool) {
	allowed := false
	if ck, err := r.Cookie("cmplz_marketing"); err == nil && ck.Value == "allow" {
		allowed = true
	}
	if !broken && !allowed {
		return
	}
	run := r.Header.Get("X-Echo-Run")
	page := "https://" + hostOnly(r.Host) + r.URL.Path
	ua := r.UserAgent()
	go func() {
		body := fmt.Sprintf(`{"data":[{"event_name":"PageView","event_time":%d,"action_source":"website","event_source_url":%q,"user_data":{"client_user_agent":%q}}]}`,
			time.Now().Unix(), page, ua)
		req, err := http.NewRequest(http.MethodPost, "https://graph.facebook.com/v19.0/"+CAPIPixel+"/events", strings.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if run != "" {
			req.Header.Set("X-Echo-Run", run)
		}
		resp, err := c.client.Do(req) //nolint:gosec // a fixed URL; the dialer reaches only Echo's proxy
		if err != nil {
			return
		}
		_ = resp.Body.Close()
	}()
}
