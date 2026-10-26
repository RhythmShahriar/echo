// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package tracker

import (
	"slices"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
)

func TestExemptHasNoCookies(t *testing.T) {
	exempt := 0
	for _, e := range builtin.entries {
		if e.info.Need != consent.NeedExempt {
			continue
		}
		exempt++
		if len(e.cookies) > 0 {
			t.Errorf("%s is exempt but lists cookies %v", e.info.ID, e.cookies)
		}
	}
	if exempt < 2 {
		t.Fatalf("%d exempt entries, want plausible and fathom at least", exempt)
	}
}

func TestPlausibleAndMatomo(t *testing.T) {
	post := func(u, body string) *consent.Request { return &consent.Request{Method: "POST", URL: u, Type: "fetch"} }
	for _, c := range []struct {
		name, url, body, want string
		need                  consent.ConsentNeed
	}{
		{"plausible", "https://plausible.io/api/event", `{"n":"pageview","u":"https://shop.test/"}`, "plausible", consent.NeedExempt},
		{"plausible proxied", "https://shop.test/api/event", `{"n":"pageview","u":"https://shop.test/"}`, "plausible", consent.NeedExempt},
		{"search API", "https://shop.test/api/event", `{"query":"x"}`, "", ""},
		{"not JSON", "https://shop.test/api/event", `n=pageview&u=x`, "", ""},
		{"JSON array", "https://shop.test/api/event", `[{"n":"pageview","u":"x"}]`, "", ""},
		{"matomo", "https://stats.shop.test/matomo.php?idsite=1&rec=1", "", "matomo", consent.NeedNeeded},
		{"matomo in the body", "https://stats.shop.test/matomo.php", "idsite=1&rec=1", "matomo", consent.NeedNeeded},
		{"matomo without parameters", "https://shop.test/matomo.php", "", "", ""},
	} {
		h := builtin.Request(post(c.url, c.body), []byte(c.body), shop)
		switch {
		case c.want == "" && h != nil:
			t.Errorf("%s: %+v, want no tracker", c.name, h)
		case c.want != "" && (h == nil || h.Tracker != c.want || h.Need != c.need):
			t.Errorf("%s: %+v, want %s (%s)", c.name, h, c.want, c.need)
		}
	}
	h := builtin.Request(post("https://shop.test/api/event", ""), []byte(`{"n":"signup","u":"https://shop.test/"}`), shop)
	if h == nil || !slices.Equal(h.Events, []string{"signup"}) || !h.FirstParty {
		t.Errorf("plausible event = %+v", h)
	}
}

func TestCookieAndStorage(t *testing.T) {
	for _, c := range []struct{ name, domain, want string }{
		{"IDE", ".doubleclick.net", "google-ads"},
		{"IDE", "shop.test", ""},
		{"MUID", ".bing.com", "microsoft-uet"},
		{"_hjSessionUser_123", "shop.test", "hotjar"},
		{"_pk_id.1.1fff", "shop.test", "matomo"},
		{"ph_phc_abc_posthog", "shop.test", "posthog"},
		{"ph_banner", "shop.test", ""},
		{"my_shop_pref", "shop.test", ""},
	} {
		h := builtin.Cookie(&consent.Cookie{Name: c.name, Domain: c.domain, Length: 12})
		if got := ""; h != nil {
			got = h.Tracker
			if got != c.want {
				t.Errorf("cookie %s on %s = %s, want %q", c.name, c.domain, got, c.want)
			}
			if h.Category == "" || h.Need == "" || h.Name == "" {
				t.Errorf("cookie %s: hit without category or need: %+v", c.name, h)
			}
		} else if c.want != "" {
			t.Errorf("cookie %s on %s = none, want %s", c.name, c.domain, c.want)
		}
	}
	if h := builtin.Storage(&consent.StorageKey{Area: "local", Key: "ajs_user_traits"}); h == nil || h.Tracker != "segment" {
		t.Errorf("storage ajs_user_traits = %+v", h)
	}
	if builtin.Cookie(nil) != nil || builtin.Storage(nil) != nil || builtin.Request(nil, nil, nil) != nil {
		t.Fatal("nil input classified")
	}
	for _, u := range []string{"", "::", "/g/collect?v=2&tid=G-X", "https://", "mailto:a@b.test"} {
		if h := builtin.Request(get(u, ""), []byte("{"), shop); h != nil {
			t.Errorf("%q = %+v", u, h)
		}
	}
}
