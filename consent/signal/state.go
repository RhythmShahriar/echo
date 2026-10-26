// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package signal

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/signal/cmpstate"
)

// Limits on what the state reader reads from a tab.
const (
	// maxPrefixCookies bounds how many cookies one prefix name reads.
	maxPrefixCookies = 10
	// maxStateJS bounds the API state a rule's state_js may return.
	maxStateJS = 64 << 10
)

// StateReader is the consent.StateReader: it reads a CMP's stored choice
// from a tab's cookies and local storage with the parser a CMP rule's
// verify spec names and asks the CMP's API when the rule gives one. It
// has no state, so the zero value is ready and safe for concurrent use.
type StateReader struct{}

var _ consent.StateReader = StateReader{}

func init() { consent.SetStateReader(StateReader{}) }

// parser returns the parser named name, the TCF one built on Decoder.
func parser(name string) (cmpstate.Parser, bool) {
	if name == "tcf" {
		return cmpstate.TCF(Decoder{}.TCF), true
	}
	p, ok := cmpstate.Parsers[name]
	return p, ok
}

// Read reads the state spec describes. For each name in spec.Cookies
// (exact, or a prefix when it ends in *, at most 10 cookies) it reads the
// cookie's value and URL-decodes it once; for each key in spec.Storage
// it reads localStorage. A spec with neither uses the parser's default
// names. The values go to the parser and are dropped: nothing returned
// or reported contains one. When spec.StateJS is set its result (at most
// 64 KiB of JSON) is read too; it wins when it says decided.
//
// Parser banner returns an undecided state with Source banner: the CMP
// driver decides from the banner. An unknown parser is the error
// consent: no state parser "<name>". A stored value the parser can't
// read gives an undecided state and an error naming the cookie or key,
// unless the API state is decided. It returns nil, nil when nothing is
// stored yet.
func (StateReader) Read(ctx context.Context, t consent.Tab, spec consent.VerifySpec) (*consent.CMPState, error) {
	if spec.Parser == "banner" {
		return &consent.CMPState{Source: "banner"}, nil
	}
	parse, ok := parser(spec.Parser)
	if !ok {
		return nil, fmt.Errorf("consent: no state parser %q", spec.Parser)
	}
	where := cmpstate.Where{Cookies: spec.Cookies, Storage: spec.Storage}
	if len(where.Cookies) == 0 && len(where.Storage) == 0 {
		where = cmpstate.Defaults[spec.Parser]
	}
	values, err := readValues(ctx, t, where)
	if err != nil {
		return nil, err
	}
	stored, perr := parse(values)
	clear(values)
	api, err := readAPI(ctx, t, spec)
	if err != nil {
		return nil, err
	}
	switch {
	case api != nil && api.Decided:
		return api, nil
	case stored != nil:
		return stored, perr
	}
	return api, nil
}

// readValues reads the cookies and storage keys w names. Empty values
// are left out, so a parser sees only what is stored.
func readValues(ctx context.Context, t consent.Tab, w cmpstate.Where) (map[string]string, error) {
	values := map[string]string{}
	var names []string
	for _, n := range w.Cookies {
		prefix, ok := strings.CutSuffix(n, "*")
		if !ok {
			names = append(names, n)
			continue
		}
		jar, err := t.Cookies(ctx)
		if err != nil {
			return nil, fmt.Errorf("consent: list cookies: %w", err)
		}
		var matched []string
		for _, c := range jar {
			if strings.HasPrefix(c.Name, prefix) && !slices.Contains(matched, c.Name) {
				matched = append(matched, c.Name)
			}
		}
		slices.Sort(matched) // the cap keeps the same cookies on every run
		names = append(names, matched[:min(len(matched), maxPrefixCookies)]...)
	}
	for _, n := range names {
		v, err := t.CookieValue(ctx, n)
		if err != nil {
			return nil, fmt.Errorf("consent: read cookie %s: %w", n, err)
		}
		if v == "" {
			continue
		}
		if d, err := url.PathUnescape(v); err == nil {
			v = d
		}
		values[n] = v
	}
	for _, k := range w.Storage {
		v, err := t.LocalStorage(ctx, k)
		if err != nil {
			return nil, fmt.Errorf("consent: read localStorage %s: %w", k, err)
		}
		if v != "" {
			values[k] = v
		}
	}
	return values, nil
}

// readAPI evaluates spec.StateJS and reads its result. A script that
// fails (the CMP's global isn't there yet) or returns too much gives no
// API state: the stored values decide. Only a canceled context is an
// error.
func readAPI(ctx context.Context, t consent.Tab, spec consent.VerifySpec) (*consent.CMPState, error) {
	if spec.StateJS == "" {
		return nil, nil
	}
	raw, err := t.EvalMain(ctx, spec.StateJS)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil || len(raw) > maxStateJS {
		return nil, nil
	}
	st, err := cmpstate.API(spec.Parser, raw)
	if err != nil {
		return nil, nil
	}
	return st, nil
}
