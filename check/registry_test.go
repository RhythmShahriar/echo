// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package check

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/compat"
	"github.com/RhythmShahriar/echo/model"
)

// ruleFixture is a message a rule must flag and a near twin it must not.
type ruleFixture struct {
	site     Options
	pos, neg model.Message
}

func mail(f func(*model.Message)) model.Message {
	m := model.Message{Channel: model.Email, Status: model.Faked, From: "orders@shop.com", To: []string{"buyer@example.com"},
		Subject: "Your order", Text: "Thanks for your order.", HTML: "<p>Thanks for your order.</p>"}
	if f != nil {
		f(&m)
	}
	return m
}

func hook(f func(*model.Message)) model.Message {
	m := model.Message{Channel: model.Webhook, Status: model.Faked, Method: "POST", URL: "https://crm.shop.com/api/leads",
		Headers: map[string]string{"content-type": "application/json"}, Body: `{"email":"a@example.com"}`}
	if f != nil {
		f(&m)
	}
	return m
}

func sms(text string, to ...string) model.Message {
	return model.Message{Channel: model.SMS, Provider: "twilio", Status: model.Faked, To: to, Text: text}
}

var shop = Options{SiteHost: "shop.com"}

// fixtures holds a positive and a negative fixture for every built-in
// rule; TestEveryRuleHasFixtures fails for a rule that has none.
var fixtures = map[string]ruleFixture{
	"send/failed": {shop, hook(func(m *model.Message) { m.Status = model.Failed; m.Response = &model.Response{Status: 500} }), hook(nil)},
	"content/placeholder": {shop, mail(func(m *model.Message) { m.Subject = "Hi {first_name}" }),
		mail(func(m *model.Message) { m.HTML = `<style>a{color:red}</style><p>{"a":1}</p>` })},
	"content/php-error": {shop,
		mail(func(m *model.Message) {
			m.Text = "Notice: Undefined index: total in /var/www/html/wp-content/themes/shop/order.php on line 12"
		}),
		mail(func(m *model.Message) { m.Text = "Notice: your order ships on Monday." })},
	"content/encoding": {shop, mail(func(m *model.Message) { m.Text = "Itâ€™s ready" }), mail(func(m *model.Message) { m.Text = "It’s ready, café" })},
	"content/dev-link": {shop, mail(func(m *model.Message) { m.HTML = `<a href="http://localhost:8080/account">Account</a>` }),
		mail(func(m *model.Message) { m.HTML = `<a href="https://shop.com/account">Account</a>` })},
	"email/no-subject": {shop, mail(func(m *model.Message) { m.Subject = " " }), mail(nil)},
	"email/empty":      {shop, mail(func(m *model.Message) { m.Text, m.HTML = "", "" }), mail(func(m *model.Message) { m.HTML = "" })},
	"email/no-text":    {shop, mail(func(m *model.Message) { m.Text = "" }), mail(nil)},
	"email/clipped":    {shop, mail(func(m *model.Message) { m.HTML = strings.Repeat("x", 103*1024) }), mail(func(m *model.Message) { m.HTML = strings.Repeat("x", 100*1024) })},
	"email/from-domain": {shop, mail(func(m *model.Message) { m.From = "orders@other.com" }),
		mail(func(m *model.Message) { m.From = "orders@mail.shop.com" })},
	"sms/number":   {shop, sms("Shipped", "015112345678"), sms("Shipped", "+4915112345678")},
	"sms/parts":    {shop, sms(strings.Repeat("a", 161), "+4915112345678"), sms(strings.Repeat("a", 160), "+4915112345678")},
	"webhook/json": {shop, hook(func(m *model.Message) { m.Body = `{"order":` }), hook(nil)},
	"webhook/signature": {shop, hook(func(m *model.Message) { m.Provider = "woocommerce" }), hook(func(m *model.Message) {
		m.Provider = "woocommerce"
		m.Headers["x-wc-webhook-signature"] = "ab•••••"
	})},
	"content/secret":      {shop, hook(func(m *model.Message) { m.Body = `{"api_key":"ab•••••"}` }), hook(nil)},
	"seal/real-recipient": {shop, mail(func(m *model.Message) { m.Status = model.Sent; m.To = []string{"jane@gmail.com"} }), mail(func(m *model.Message) { m.Status = model.Sent })},
	"compat/html": {shop, mail(func(m *model.Message) { m.HTML = `<div style="display: grid">Your order</div>` }),
		mail(func(m *model.Message) { m.HTML = `<table><tr><td style="color: #333">Your order</td></tr></table>` })},
	"compat/spam": {shop, mail(func(m *model.Message) {
		m.Headers = map[string]string{SpamHeader: SpamValue(compat.SpamResult{Score: 6.2, Threshold: 5, Spam: true, Rules: []string{"GTUBE"}}, nil)}
	}), mail(nil)},
}

func checksOf(m model.Message, o Options) []string {
	var out []string
	for _, i := range Message(m, o) {
		out = append(out, i.Check)
	}
	return out
}

func TestEveryRuleHasFixtures(t *testing.T) {
	for _, r := range Rules() {
		f, ok := fixtures[r.ID]
		if !ok {
			t.Errorf("rule %s has no fixtures: add a positive and a negative message to fixtures", r.ID)
			continue
		}
		if r.Doc == "" || r.Level == "" {
			t.Errorf("rule %s needs a Doc and a Level", r.ID)
		}
		if got := checksOf(f.pos, f.site); !slices.Contains(got, r.ID) {
			t.Errorf("%s: the positive fixture isn't flagged (got %v)", r.ID, got)
		}
		if got := checksOf(f.neg, f.site); slices.Contains(got, r.ID) {
			t.Errorf("%s: the negative fixture is flagged", r.ID)
		}
	}
	for id := range fixtures {
		if _, ok := Lookup(id); !ok {
			t.Errorf("fixtures for %s, which isn't a registered rule", id)
		}
	}
}

func TestRegisterRefusesDuplicatesAndBlanks(t *testing.T) {
	for name, r := range map[string]Rule{
		"duplicate": {ID: "send/failed", Run: sendFailed.Run},
		"no id":     {Run: sendFailed.Run},
		"no run":    {ID: "x/y"},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: Register didn't panic", name)
				}
			}()
			Register(r)
		}()
	}
}

func TestWriteRulesListsEveryRule(t *testing.T) {
	var b strings.Builder
	if err := WriteRules(&b, Rules()); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, r := range Rules() {
		if !strings.Contains(out, r.ID) {
			t.Errorf("%s missing from:\n%s", r.ID, out)
		}
	}
	if !regexp.MustCompile(`(?m)^sms/parts +notice +sms +An SMS needs`).MatchString(out) {
		t.Errorf("columns:\n%s", out)
	}
}

var oldPhone = Custom{ID: "brand/old-phone", Channels: []model.Channel{model.Email, model.SMS}, Regex: `0800[ -]?123[ -]?456`,
	Message: "Old phone number", Fixtures: Fixtures{Match: []string{"Call 0800 123 456", "Call 0800-123-456"}, NoMatch: []string{"Call 0800 123 457"}}}

func TestCustomRules(t *testing.T) {
	rules, err := CustomRules([]Custom{oldPhone, {ID: "brand/rival", Match: "MegaShop"}})
	if err != nil {
		t.Fatal(err)
	}
	o := Options{SiteHost: "shop.com", Rules: rules}
	is := Message(mail(func(m *model.Message) { m.Text = "Questions? Call 0800 123 456." }), o)
	i := slices.IndexFunc(is, func(i model.Issue) bool { return i.Check == "brand/old-phone" })
	if i < 0 {
		t.Fatalf("the custom rule didn't fire: %s", titles(is))
	}
	if got := is[i]; got.Level != model.Warning || got.Title != "Old phone number in email" || got.Detail != `found "0800 123 456"` ||
		got.Key != "brand/old-phone|Old phone number in email" {
		t.Errorf("issue: %+v", got)
	}
	// The subject and the body count; a channel the rule doesn't list
	// doesn't.
	if got := checksOf(mail(func(m *model.Message) { m.Subject = "Visit MegaShop" }), o); !slices.Contains(got, "brand/rival") {
		t.Errorf("subject: %v", got)
	}
	if got := checksOf(hook(func(m *model.Message) { m.Body = `{"phone":"0800 123 456"}` }), o); slices.Contains(got, "brand/old-phone") {
		t.Errorf("a webhook isn't on the rule's channels: %v", got)
	}
	if got := checksOf(hook(func(m *model.Message) { m.Body = `{"shop":"MegaShop"}` }), o); !slices.Contains(got, "brand/rival") {
		t.Errorf("body: %v", got)
	}
	if got := checksOf(mail(nil), o); slices.ContainsFunc(got, func(c string) bool { return strings.HasPrefix(c, "brand/") }) {
		t.Errorf("a clean message: %v", got)
	}
}

func TestCustomRuleErrors(t *testing.T) {
	for want, cs := range map[string][]Custom{
		`rule "send/failed": that is a built-in check`:  {{ID: "send/failed", Match: "x"}},
		`rule "message/text": that is a built-in check`: {{ID: "message/text", Match: "x"}},
		`rule "dns/spf": that is a built-in check`:      {{ID: "dns/spf", Match: "x"}},
		`rule "run/x": that is a built-in check`:        {{ID: "run/x", Match: "x"}},
		`rule "a b": needs an id without spaces`:        {{ID: "a b", Match: "x"}},
		`rule "a": set exactly one of match or regex`:   {{ID: "a", Match: "x", Regex: "y"}},
		`rule "a": regex: error parsing regexp`:         {{ID: "a", Regex: "("}},
		`rule "a": level "broke" isn't notice, warning`: {{ID: "a", Match: "x", Level: "broke"}},
		`rule "a" is listed twice`:                      {{ID: "a", Match: "x"}, {ID: "a", Match: "y"}},
	} {
		if _, err := CustomRules(cs); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want %q, got %v", want, err)
		}
	}
}

func TestCustomFixtures(t *testing.T) {
	var b strings.Builder
	ok, err := WriteFixtures(&b, []Custom{oldPhone})
	if err != nil || !ok {
		t.Fatalf("declared fixtures must pass (%v):\n%s", err, b.String())
	}
	if want := "ok    brand/old-phone matches \"Call 0800 123 456\"\n"; !strings.Contains(b.String(), want) {
		t.Errorf("want %q in:\n%s", want, b.String())
	}
	if !strings.HasSuffix(b.String(), "1 rule, 3 fixtures, all pass.\n") {
		t.Errorf("summary:\n%s", b.String())
	}

	wrong := oldPhone
	wrong.Fixtures = Fixtures{Match: []string{"Call 0800 999 999"}, NoMatch: []string{"Call 0800 123 456"}}
	missing := Custom{ID: "brand/rival", Match: "MegaShop", Fixtures: Fixtures{Match: []string{"MegaShop"}}}
	bad := Custom{ID: "brand/bad", Regex: "("}
	b.Reset()
	ok, _ = WriteFixtures(&b, []Custom{wrong, missing, bad})
	if ok {
		t.Error("wrong fixtures must fail")
	}
	for _, want := range []string{
		`FAIL  brand/old-phone should match "Call 0800 999 999" but doesn't`,
		`FAIL  brand/old-phone shouldn't match "Call 0800 123 456" but does`,
		`ok    brand/rival matches "MegaShop"`,
		`FAIL  brand/rival: has no no_match fixture; every rule needs one that matches and one that doesn't`,
		`FAIL  brand/bad: rule "brand/bad": regex: error parsing regexp`,
		"3 rules, 3 fixtures, 4 failed.",
	} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("want %q in:\n%s", want, b.String())
		}
	}
}

func TestChecksOverrideLevelsAndTurnRulesOff(t *testing.T) {
	m := mail(func(m *model.Message) {
		m.Subject = "Hi {first_name}"
		m.HTML = `<a href="http://localhost/x">x</a>`
	})
	rules, err := CustomRules([]Custom{{ID: "brand/rival", Match: "Hi"}})
	if err != nil {
		t.Fatal(err)
	}
	o := Options{SiteHost: "shop.com", Rules: rules, Checks: map[string]string{
		"content/dev-link": "off", "content/placeholder": "warning", "brand/rival": "broke",
	}}
	levels := map[string]model.Level{}
	for _, i := range Message(m, o) {
		levels[i.Check] = i.Level
	}
	if _, found := levels["content/dev-link"]; found {
		t.Error("an off check must report nothing")
	}
	if levels["content/placeholder"] != model.Warning || levels["brand/rival"] != model.Error {
		t.Errorf("levels: %v", levels)
	}
}

func TestCompatIssues(t *testing.T) {
	m := mail(func(m *model.Message) { m.HTML = "<p>a</p>\n<div style=\"display: grid\">b</div>" })
	is := compatHTML.Run(m, shop)
	if len(is) != 1 || !strings.HasPrefix(is[0].Title, "Email uses display: grid, which Outlook for Windows") ||
		!strings.Contains(is[0].Detail, "line 2") || is[0].Fix == "" {
		t.Errorf("html: %+v", is)
	}
	for _, c := range []struct {
		value, title, detail string
	}{
		{SpamValue(compat.SpamResult{Score: 2.1, Threshold: 5, Rules: []string{"HTML_MESSAGE", "MISSING_DATE"}}, nil),
			"SpamAssassin scores the email 2.1 of 5.0", "rules: HTML_MESSAGE, MISSING_DATE"},
		{SpamValue(compat.SpamResult{Score: 15.2, Threshold: 5, Spam: true}, nil), "SpamAssassin calls the email spam (15.2 of 5.0)", "no rules hit"},
		{SpamValue(compat.SpamResult{}, errors.New("spamassassin at 127.0.0.1:783: couldn't connect: refused; again")),
			"SpamAssassin couldn't score the email", "spamassassin at 127.0.0.1:783: couldn't connect: refused, again"},
	} {
		m := mail(func(m *model.Message) { m.Headers = map[string]string{SpamHeader: c.value} })
		is := compatSpam.Run(m, shop)
		if len(is) != 1 || is[0].Title != c.title || is[0].Detail != c.detail {
			t.Errorf("%s: %+v", c.value, is)
		}
	}
}

// TestCustomRuleDetailIsMasked: a rule that catches a leaked key doesn't
// copy the key into the report.
func TestCustomRuleDetailIsMasked(t *testing.T) {
	key := "sk_" + "live_51HxQ2eLkV9pZr8mN4bT6wY3"
	rs, err := CustomRules([]Custom{{ID: "leak/stripe-key", Regex: `sk_live_\w+`, Level: model.Error}})
	if err != nil {
		t.Fatal(err)
	}
	is := Message(mail(func(m *model.Message) { m.Text = "Debug: " + key }), Options{Rules: rs})
	for _, i := range is {
		if i.Check == "leak/stripe-key" {
			if strings.Contains(i.Detail, key) || !strings.HasPrefix(i.Detail, "found ") {
				t.Errorf("detail %q", i.Detail)
			}
			return
		}
	}
	t.Fatalf("the rule didn't fire: %+v", is)
}
