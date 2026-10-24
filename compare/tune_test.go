package compare

import (
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/check"
	"github.com/RhythmShahriar/echo/model"
)

func order(msgs ...model.Message) model.Action {
	return model.Action{ID: "completed-order", Label: "Completed order", Kind: "command", Messages: msgs}
}

// withIssues runs the checks over an action's messages, as the runner
// does, with overrides.
func withIssues(a model.Action, checks map[string]string) model.Action {
	for _, m := range a.Messages {
		a.Issues = append(a.Issues, check.Message(m, check.Options{SiteHost: "shop.com", Checks: checks})...)
	}
	return a
}

func crmHook(body string) model.Message {
	return model.Message{ID: model.NewID(body), Channel: model.Webhook, Status: model.Faked, Method: "POST", URL: "https://crm.shop.com/api/orders",
		Headers: map[string]string{"content-type": "application/json"}, Body: body}
}

func TestFieldsLostIsBrokeForWebhooksAndPayments(t *testing.T) {
	for ch, class := range map[model.Channel]string{model.Webhook: model.Broke, model.Payment: model.Broke, model.Analytics: model.Changed} {
		b, a := crmHook(`{"id":1,"total":"9.00"}`), crmHook(`{"id":1}`)
		b.Channel, a.Channel = ch, ch
		cs := Messages([]model.Message{b}, []model.Message{a})
		i := slices.IndexFunc(cs, func(c model.Change) bool { return c.Check == "message/fields-lost" })
		if i < 0 || cs[i].Class != class {
			t.Errorf("%s: want fields-lost %s:\n%s", ch, class, titles(cs))
		}
	}
}

func TestOverridesMoveAndHideChecks(t *testing.T) {
	analytics := func(body string) model.Message {
		m := crmHook(body)
		m.Channel, m.URL = model.Analytics, "https://www.google-analytics.com/mp/collect"
		return m
	}
	link := func(href string) model.Message {
		return model.Message{ID: href, Channel: model.Email, Status: model.Faked, From: "o@shop.com", To: []string{"buyer@example.com"},
			Subject: "Receipt", Text: "Your receipt " + href}
	}
	checks := map[string]string{"message/fields-lost": "broke", "content/dev-link": "off"}
	before := run(withIssues(order(analytics(`{"a":1,"b":2}`), link("https://shop.com/r")), checks))
	after := run(withIssues(order(analytics(`{"a":1}`), link("http://localhost/r")), checks))

	// Without the overrides: fields-lost on analytics is CHANGED and the
	// dev link is NEW.
	plainBefore := run(withIssues(order(analytics(`{"a":1,"b":2}`), link("https://shop.com/r")), nil))
	plainAfter := run(withIssues(order(analytics(`{"a":1}`), link("http://localhost/r")), nil))
	plain := titles(Compare(plainBefore, plainAfter, "", "", "test").Changes)
	if !strings.Contains(plain, "changed: Event: payload lost fields") ||
		!strings.Contains(plain, "new: Email links to a development address (localhost)") {
		t.Fatalf("without overrides:\n%s", plain)
	}

	got := Options{Checks: checks}.Compare(before, after, "", "", "test")
	gotT := titles(got.Changes)
	if !strings.Contains(gotT, "broke: Event: payload lost fields") {
		t.Errorf("fields-lost should be raised to BROKE:\n%s", gotT)
	}
	if strings.Contains(gotT, "development address") {
		t.Errorf("content/dev-link is off:\n%s", gotT)
	}
	if slices.ContainsFunc(after.Actions[0].Issues, func(i model.Issue) bool { return i.Check == "content/dev-link" }) {
		t.Error("check.Message must drop an issue whose check is off")
	}
	// Off when comparing also hides an issue a run recorded before the
	// override was set.
	off := Options{Checks: map[string]string{"content/dev-link": "off"}}
	if got := titles(off.Compare(plainBefore, plainAfter, "", "", "test").Changes); strings.Contains(got, "development address") {
		t.Errorf("an old run's dev-link issue must be hidden too:\n%s", got)
	}
}

func TestOverrideClasses(t *testing.T) {
	o := Options{Checks: map[string]string{"a": "changed", "b": "warning", "c": "ERROR", "d": "off"}}
	for _, tc := range []struct {
		in   model.Change
		want string
		on   bool
	}{
		{model.Change{Class: model.New, Check: "a"}, model.Changed, true},
		{model.Change{Class: model.Broke, Check: "b"}, model.New, true},
		{model.Change{Class: model.New, Check: "c"}, model.Broke, true},
		{model.Change{Class: model.Fixed, Check: "c"}, model.Fixed, true},
		{model.Change{Class: model.Fixed, Check: "d"}, model.Fixed, false},
		{model.Change{Class: model.New, Check: "other"}, model.New, true},
	} {
		c, on := o.tune(tc.in)
		if c.Class != tc.want || on != tc.on {
			t.Errorf("%s %s: got %s %v, want %s %v", tc.in.Class, tc.in.Check, c.Class, on, tc.want, tc.on)
		}
	}
}

func TestIgnoreLeavesOutFieldsHeadersAndRecipients(t *testing.T) {
	b := crmHook(`{"id":1,"modified":"2026-10-01","meta":{"a":1},"items":[{"sku":"A","cache":1}]}`)
	a := crmHook(`{"id":1,"items":[{"sku":"A"}]}`)
	mailB := email("admin@shop.com", "New order", "Order received")
	mailB.CC = []string{"pm@agency.example"}
	mailA := email("admin@shop.com", "New order", "Order received")
	before, after := run(order(b, mailB)), run(order(a, mailA))

	got := titles(Compare(before, after, "", "", "test").Changes)
	if !strings.Contains(got, "payload lost fields") || !strings.Contains(got, "copies (Cc/Bcc) changed") {
		t.Fatalf("without ignore:\n%s", got)
	}
	for _, key := range []string{"completed-order", "Completed order"} {
		o := Options{Ignore: map[string][]string{key: {"modified", "meta", "items[].cache", "*@agency.example", "x-request-id"}}}
		c := o.Compare(before, after, "", "", "test")
		if len(c.Changes) != 0 {
			t.Errorf("ignored by %q, still reported:\n%s", key, titles(c.Changes))
		}
	}
	// A field that isn't ignored is still reported.
	o := Options{Ignore: map[string][]string{"completed-order": {"modified"}}}
	if got := titles(o.Compare(before, after, "", "", "test").Changes); !strings.Contains(got, "payload lost fields") {
		t.Errorf("meta and items[].cache are still lost:\n%s", got)
	}
	// The runs themselves are untouched.
	if before.Actions[0].Messages[1].CC[0] != "pm@agency.example" || !strings.Contains(before.Actions[0].Messages[0].Body, "modified") {
		t.Error("strip changed the stored run")
	}
}

func TestStripFields(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"a":1,"b":{"c":2,"d":3}}`, `{"a":1,"b":{"d":3}}`},
		{`{"a":1}`, `{"a":1}`},
		{`{"a": 1}`, `{"a": 1}`}, // nothing ignored: as sent
		{`[{"c":1,"x":2}]`, `[{"x":2}]`},
		{`a=1&token=2`, `a=1`},
		{`not json`, `not json`},
	} {
		if got := stripFields(tc.body, []string{"b.c", "[].c", "token"}); got != tc.want {
			t.Errorf("stripFields(%s) = %s, want %s", tc.body, got, tc.want)
		}
	}
}

func TestAcceptRemovesAChangeByKey(t *testing.T) {
	before := run(order(email("admin@shop.com", "New order", "Order received")))
	after := run(order(email("admin@shop.com", "A new order", "Order received")))
	c := Compare(before, after, "", "", "test")
	if len(c.Changes) != 1 {
		t.Fatalf("want one change:\n%s", titles(c.Changes))
	}
	key := Key(c.Changes[0])
	if len(key) != 8 || key != Key(Compare(before, after, "", "", "test").Changes[0]) {
		t.Fatalf("key %q isn't stable", key)
	}
	got := Options{Accept: []string{strings.ToUpper(key)}}.Compare(before, after, "", "", "test")
	if len(got.Changes) != 0 {
		t.Errorf("accepted change still listed:\n%s", titles(got.Changes))
	}
	if want := "accepted as intended: Email to admin@shop.com: subject changed (" + key + ")"; !slices.Contains(got.Warnings, want) {
		t.Errorf("want note %q in %q", want, got.Warnings)
	}
	accepted, unknown := Accept(c, []string{key, "deadbeef"})
	if len(accepted) != 1 || !slices.Equal(unknown, []string{"deadbeef"}) {
		t.Errorf("accepted %v, unknown %v", accepted, unknown)
	}
	// Another edit of the same text is another key.
	other := run(order(email("admin@shop.com", "Order in", "Order received")))
	if k := Key(Compare(before, other, "", "", "test").Changes[0]); k == key {
		t.Error("a different subject must get a different key")
	}
}

func TestPlaceholderSMSIsReportedOnce(t *testing.T) {
	sms := func(text string) model.Message {
		return model.Message{ID: model.NewID(text), Channel: model.SMS, Provider: "twilio", Status: model.Faked, To: []string{"+15551234567"}, Text: text}
	}
	before := run(withIssues(order(sms("Hi Jamie, your order shipped.")), nil))
	after := run(withIssues(order(sms("Hi {first_name}, your order shipped.")), nil))
	c := Compare(before, after, "", "", "test")
	if len(c.Changes) != 1 || c.Changes[0].Class != model.Broke || c.Changes[0].Check != "content/placeholder" {
		t.Errorf("want one BROKE placeholder:\n%s", titles(c.Changes))
	}
	// A text change with no placeholder is still reported.
	after = run(withIssues(order(sms("Hello Jamie, your order shipped.")), nil))
	if got := titles(Compare(before, after, "", "", "test").Changes); !strings.Contains(got, "changed: Twilio SMS to the test number: text changed") {
		t.Errorf("a plain text change:\n%s", got)
	}
	// With the placeholder check lowered to a warning it's NEW, so the text
	// change stays.
	o := Options{Checks: map[string]string{"content/placeholder": "warning"}}
	after = run(withIssues(order(sms("Hi {first_name}, your order shipped.")), nil))
	if got := titles(o.Compare(before, after, "", "", "test").Changes); !strings.Contains(got, "text changed") {
		t.Errorf("a placeholder that isn't BROKE doesn't cover the text:\n%s", got)
	}
}

func TestUnknownChecks(t *testing.T) {
	rules, err := check.CustomRules([]check.Custom{{ID: "brand/old-phone", Match: "0800"}})
	if err != nil {
		t.Fatal(err)
	}
	got := UnknownChecks(map[string]string{
		"content/dev-link": "off", "message/fields-lost": "broke", "dns/spf": "warning", "brand/old-phone": "error",
		"content/devlink": "off", "fields-lost": "broke",
	}, rules)
	if !slices.Equal(got, []string{"content/devlink", "fields-lost"}) {
		t.Errorf("got %v", got)
	}
}
