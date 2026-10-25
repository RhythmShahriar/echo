// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package recognize

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/RhythmShahriar/echo/model"
)

// declaredCases turns the YAML providers' fixtures into the same tables
// the Go recognizers use, so every suite (claims, overreach, fakes, the
// fixture rule) covers them too.
func declaredCases() []tcase {
	var out []tcase
	for _, p := range declared {
		for _, f := range p.positive {
			out = append(out, tcase{name: f.Name, provider: p.rec.Provider, req: f.req, status: FakeFor(p.rec, f.req, nil).Status,
				check: func(t *testing.T, m model.Message) { checkWant(t, f.Want, m) }})
		}
	}
	return out
}

func declaredNegatives() []negative {
	var out []negative
	for _, p := range declared {
		for _, f := range p.negative {
			out = append(out, negative{f.Name, p.rec.Provider, f.req})
		}
	}
	return out
}

// checkWant compares a fixture's want block with what was filled in.
func checkWant(t *testing.T, want map[string]any, m model.Message) {
	t.Helper()
	got := map[string]any{"channel": string(m.Channel), "to": m.To, "cc": m.CC, "bcc": m.BCC, "from": m.From,
		"reply_to": m.ReplyTo, "subject": m.Subject, "text": m.Text, "html": m.HTML, "event": m.Event}
	for k, w := range want {
		if list, ok := w.([]any); ok {
			ws := make([]string, len(list))
			for i, v := range list {
				ws[i] = fmt.Sprint(v)
			}
			eqList(t, k, got[k].([]string), ws...)
			continue
		}
		eq(t, k, fmt.Sprint(got[k]), fmt.Sprint(w))
	}
}

func TestDeclaredProvidersShip(t *testing.T) {
	var names []string
	for _, p := range declared {
		names = append(names, p.rec.Provider)
	}
	for _, want := range []string{"mailjet", "mixpanel", "segment", "posthog", "expo", "sinch", "infobip", "bandwidth", "mailersend"} {
		if !slices.Contains(names, want) {
			t.Errorf("YAML provider %q not loaded (have %q)", want, names)
		}
	}
}

// TestDeclaredProvidersDontShadowGo: a YAML provider's slug is its own,
// never one a Go recognizer already uses.
func TestDeclaredProvidersDontShadowGo(t *testing.T) {
	for _, p := range declared {
		n := 0
		for _, r := range registry {
			if r.Provider == p.rec.Provider {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s: provider %q has %d recognizers; a Go one uses the slug too", p.file, p.rec.Provider, n)
		}
	}
}

const goodProvider = `provider: acme
name: Acme
channel: sms
host: api.acme.test
path: /v1/*/send
to: $.to
reply: {status: 202, body: {"id": "${id}"}}
`

const goodFixtures = `positive:
  - name: send
    url: https://api.acme.test/v1/abc/send
    body: {to: "+15550123"}
negative:
  - name: other path
    url: https://api.acme.test/v1/abc/list
`

func load(files map[string]string) error {
	fsys := fstest.MapFS{}
	for name, body := range files {
		fsys["p/"+name] = &fstest.MapFile{Data: []byte(body)}
	}
	_, err := loadProviders(fsys, "p")
	return err
}

func TestLoaderAcceptsAGoodProvider(t *testing.T) {
	fsys := fstest.MapFS{
		"p/acme.yaml":          {Data: []byte(goodProvider)},
		"p/acme.fixtures.yaml": {Data: []byte(goodFixtures)},
	}
	ps, err := loadProviders(fsys, "p")
	if err != nil || len(ps) != 1 {
		t.Fatalf("load: %v %v", ps, err)
	}
	rec := ps[0].rec
	var m model.Message
	r := ps[0].positive[0].req
	rec.Fill(r, &m)
	eqList(t, "to", m.To, "+15550123")
	f := rec.Fake(r, &m)
	if f.Status != 202 || f.Headers["content-type"] != "application/json" || strings.Contains(f.Body, "${id}") || len(f.Body) < 20 {
		t.Errorf("fake = %+v", f)
	}
}

// TestLoaderRefusesMissingFixtures is the fixture rule for YAML: no
// provider loads without a positive and a negative request.
func TestLoaderRefusesMissingFixtures(t *testing.T) {
	for _, c := range []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"no fixture file", map[string]string{"acme.yaml": goodProvider},
			"p/acme.yaml: no fixtures: add acme.fixtures.yaml"},
		{"no negative", map[string]string{"acme.yaml": goodProvider, "acme.fixtures.yaml": strings.Split(goodFixtures, "negative:")[0]},
			"p/acme.fixtures.yaml:1: acme needs at least one positive and one negative fixture (has 1 and 0)"},
		{"no positive", map[string]string{"acme.yaml": goodProvider, "acme.fixtures.yaml": "negative:" + strings.Split(goodFixtures, "negative:")[1]},
			"acme needs at least one positive and one negative fixture (has 0 and 1)"},
		{"positive that doesn't match", map[string]string{"acme.yaml": goodProvider,
			"acme.fixtures.yaml": strings.Replace(goodFixtures, "abc/send", "abc/sent", 1)},
			`p/acme.fixtures.yaml:2: positive fixture "send" doesn't match acme`},
		{"negative that matches", map[string]string{"acme.yaml": goodProvider,
			"acme.fixtures.yaml": strings.Replace(goodFixtures, "abc/list", "xyz/send", 1)},
			`p/acme.fixtures.yaml:6: negative fixture "other path" matches acme`},
		{"fixtures without a provider", map[string]string{"acme.yaml": goodProvider, "acme.fixtures.yaml": goodFixtures, "zeta.fixtures.yaml": goodFixtures},
			"p/zeta.fixtures.yaml: fixtures for a provider that doesn't exist"},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := load(c.files)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want %q", err, c.want)
			}
		})
	}
}

// TestLoaderNamesFileAndLine: a broken provider fails with where.
func TestLoaderNamesFileAndLine(t *testing.T) {
	for _, c := range []struct {
		name, provider, want string
	}{
		{"bad YAML", goodProvider + "\tbad: 1\n", "p/acme.yaml:8: found character that cannot start any token"},
		{"unknown key", goodProvider + "hosts: x\n", "p/acme.yaml:8: field hosts not found"},
		{"unknown channel", strings.Replace(goodProvider, "channel: sms", "channel: pager", 1), `p/acme.yaml:3: unknown channel "pager"`},
		{"payment in YAML", strings.Replace(goodProvider, "channel: sms", "channel: payment", 1), "p/acme.yaml:3: payment providers are written in Go"},
		{"bad path source", strings.Replace(goodProvider, "to: $.to", "to: $.to[x]", 1), `p/acme.yaml:6: to: "$.to[x]": [x] wants a number or *`},
		{"unknown source", strings.Replace(goodProvider, "to: $.to", "to: cookie:a", 1), `p/acme.yaml:6: to: "cookie:a": unknown source "cookie"`},
		{"slug and file differ", strings.Replace(goodProvider, "provider: acme", "provider: acme-sms", 1), `p/acme.yaml:1: provider "acme-sms" must match the file name`},
		{"relative path", strings.Replace(goodProvider, "path: /v1/*/send", "path: v1/send", 1), `p/acme.yaml:5: path "v1/send" must start with /`},
		{"bad status", strings.Replace(goodProvider, "status: 202", "status: 2020", 1), "p/acme.yaml:7: reply status 2020"},
		{"no host", strings.Replace(goodProvider, "host: api.acme.test\n", "", 1), "p/acme.yaml:1: host is required"},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := load(map[string]string{"acme.yaml": c.provider, "acme.fixtures.yaml": goodFixtures})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want %q", err, c.want)
			}
		})
	}
}

func TestJSONPathLite(t *testing.T) {
	body := map[string]any{"a": []any{map[string]any{"b": "x, y"}, map[string]any{"b": float64(2)}}, "c": true}
	for _, c := range []struct {
		path string
		want []string
	}{
		{"$.a[*].b", []string{"x, y", "2"}},
		{"$.a[1].b", []string{"2"}},
		{"$.a[5].b", nil},
		{"$.c", []string{"true"}},
		{"$.missing", nil},
		{"$[*].b", nil},
	} {
		s, err := parseSource(c.path)
		if err != nil {
			t.Fatal(err)
		}
		eqList(t, c.path, s.eval(Request{}, body), c.want...)
	}
	r := req("POST", "https://x.test/a/b/c?p=1", map[string]string{"x-h": "v"}, "f=2")
	for src, want := range map[string]string{"path:0": "a", "path:-1": "c", "query:p": "1", "header:X-H": "v", "form:f": "2"} {
		s, err := parseSource(src)
		if err != nil {
			t.Fatal(err)
		}
		eqList(t, src, s.eval(r, nil), want)
	}
}

// TestCatalog lists every provider once, by channel, with the hosts it
// claims.
func TestCatalog(t *testing.T) {
	cat := Catalog()
	if len(cat) != len(Providers()) {
		t.Errorf("catalog has %d providers, Providers %d", len(cat), len(Providers()))
	}
	for i := 1; i < len(cat); i++ {
		if cat[i-1].Channel > cat[i].Channel || cat[i-1].Channel == cat[i].Channel && cat[i-1].Provider >= cat[i].Provider {
			t.Errorf("%s before %s", cat[i-1].Provider, cat[i].Provider)
		}
	}
	byName := map[string]Entry{}
	for _, e := range cat {
		byName[e.Provider] = e
		if len(e.Hosts) == 0 && e.Provider != "woocommerce" {
			t.Errorf("%s lists no hosts", e.Provider)
		}
	}
	if e := byName["vonage"]; !slices.Equal(e.Hosts, []string{"rest.nexmo.com", "api.nexmo.com", "messages-sandbox.nexmo.com"}) || e.Declared {
		t.Errorf("vonage = %+v", e)
	}
	if e := byName["mailjet"]; e.Name != "Mailjet" || e.Channel != model.Email || !e.Declared {
		t.Errorf("mailjet = %+v", e)
	}
}

// TestHostsCoverEveryFixture keeps Recognizer.Hosts honest: the host of
// every positive fixture is one its provider lists.
func TestHostsCoverEveryFixture(t *testing.T) {
	hosts := map[string][]string{}
	for _, e := range Catalog() {
		hosts[e.Provider] = e.Hosts
	}
	for _, c := range allCases() {
		if c.provider == "woocommerce" {
			continue
		}
		if !slices.ContainsFunc(hosts[c.provider], func(h string) bool { return hostCovered(h, c.req.URL) }) {
			t.Errorf("%s/%s: host %s is not in %q", c.provider, c.name, c.req.URL.Host, hosts[c.provider])
		}
	}
}
