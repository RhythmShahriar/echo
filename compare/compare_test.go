package compare

import (
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/model"
)

func email(to, subject, text string) model.Message {
	return model.Message{Channel: model.Email, Status: model.Faked, From: "shop@shop.com", To: []string{to}, Subject: subject, Text: text}
}

func run(actions ...model.Action) *model.Run {
	return &model.Run{Schema: model.Schema, Mode: "seal", Actions: actions}
}

func titles(cs []model.Change) string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Class+": "+c.Title)
	}
	return strings.Join(out, "\n")
}

func TestContactFormStopsEmailingAdmin(t *testing.T) {
	form := func(msgs ...model.Message) model.Action {
		return model.Action{ID: "form:/contact/#wpcf7-f1", Label: "Contact form on /contact/", Kind: "form", Messages: msgs}
	}
	before := run(form(
		email("admin@shop.com", "New message from soundcheck-test 3f2a", "Name: soundcheck-test 3f2a\nMessage: hello"),
		email("soundcheck-test+3f2a@example.com", "Thanks for your message", "We'll reply within 2 days."),
	))
	after := run(form(
		email("soundcheck-test+9c1d@example.com", "Thanks for your message", "We'll reply within 2 days."),
	))
	c := Compare(before, after, "", "", "test")
	got := titles(c.Changes)
	if !strings.Contains(got, "broke: Email to admin@shop.com is no longer sent") {
		t.Errorf("want the admin email reported as BROKE:\n%s", got)
	}
	if strings.Contains(got, "test submitter") {
		t.Errorf("the autoresponder to the (per-run) test address is the same message:\n%s", got)
	}
	if c.Actions != 1 {
		t.Errorf("actions = %d", c.Actions)
	}
}

func TestChangesWithinAMessage(t *testing.T) {
	b := []model.Message{
		{Channel: model.Webhook, Provider: "", Method: "POST", URL: "https://hooks.zapier.com/hooks/catch/123/abc/", Status: model.Faked,
			Body: `{"order":{"id":42,"total":"19.00","email":"x@example.com"},"items":[{"sku":"A"}]}`},
		email("admin@shop.com", "Order #1042 received", "Total: 19.00"),
	}
	a := []model.Message{
		{Channel: model.Webhook, Method: "POST", URL: "https://hooks.zapier.com/hooks/catch/123/abc/", Status: model.Faked,
			Body: `{"order":{"id":43,"email":"y@example.com"},"items":[{"sku":"B"}]}`},
		email("admin@shop.com", "Order #1043 received", "Total: 25.00"),
	}
	got := titles(Messages(b, a))
	if !strings.Contains(got, "changed: Webhook to hooks.zapier.com: payload lost fields") {
		t.Errorf("lost field not reported:\n%s", got)
	}
	if strings.Contains(got, "subject changed") || strings.Contains(got, "text changed") {
		t.Errorf("order numbers and amounts aren't changes:\n%s", got)
	}
}

func TestFailingAndNew(t *testing.T) {
	sms := model.Message{Channel: model.SMS, Provider: "twilio", To: []string{"+15551234567"}, Text: "Shipped", Status: model.Faked}
	failed := sms
	failed.Status, failed.Error = model.Failed, "auth error"
	slack := model.Message{Channel: model.Chat, Provider: "slack", URL: "https://hooks.slack.com/services/x", Method: "POST", Status: model.Faked}
	got := titles(Messages([]model.Message{sms}, []model.Message{failed, slack}))
	for _, want := range []string{"broke: Twilio SMS to the test number now fails", "new: Slack message is now sent"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}

func TestGroupingAcrossActions(t *testing.T) {
	mk := func(id string, msgs ...model.Message) model.Action {
		return model.Action{ID: id, Label: id, Messages: msgs}
	}
	m := email("admin@shop.com", "New lead", "x")
	before := run(mk("a", m), mk("b", m), mk("c", m))
	after := run(mk("a"), mk("b"), mk("c"))
	c := Compare(before, after, "", "", "test")
	if len(c.Changes) != 1 || len(c.Changes[0].Actions) != 3 {
		t.Errorf("want one change across 3 actions: %+v", c.Changes)
	}
}

func TestIssuesDiff(t *testing.T) {
	i := func(level model.Level, title string) model.Issue {
		x := model.Issue{Check: "content/x", Level: level, Title: title}
		x.Identify()
		return x
	}
	got := titles(Issues([]model.Issue{i(model.Warning, "old")}, []model.Issue{i(model.Error, "placeholder"), i(model.Notice, "note")}))
	for _, want := range []string{"broke: placeholder", "new: note", "fixed: old"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}

func TestPathPatternAndFields(t *testing.T) {
	if p := pathPattern("/v3/orders/123/notes/9f8e7d6c5b4a"); p != "/v3/orders/{id}/notes/{id}" {
		t.Errorf("pathPattern = %q", p)
	}
	f := strings.Join(fields(`{"a":{"b":1},"c":[{"d":2}]}`), ",")
	if f != "a,a.b,c,c[].d" {
		t.Errorf("fields = %q", f)
	}
	if f := strings.Join(fields("To=%2B1&Body=hi"), ","); f != "Body,To" {
		t.Errorf("form fields = %q", f)
	}
}

func TestSeveralEmailsToOneAddress(t *testing.T) {
	welcome := email("echo-test+1@example.com", "Welcome to Shop", "Hello")
	welcome.Provider = "resend"
	account := email("echo-test+1@example.com", "Your account is ready", "Login here")
	verify := email("echo-test+1@example.com", "Verify your email", "Click")
	// Lost one of two emails to the same address (and provider).
	got := titles(Messages([]model.Message{account, verify}, []model.Message{account}))
	if !strings.Contains(got, `broke: Email to the test submitter ("Verify your email") is no longer sent`) || strings.Contains(got, "subject changed") {
		t.Errorf("two emails, one lost:\n%s", got)
	}
	// A different provider is a different message.
	got = titles(Messages([]model.Message{welcome, account}, []model.Message{account}))
	if !strings.Contains(got, "broke: Resend email to the test submitter is no longer sent") || strings.Contains(got, "changed") {
		t.Errorf("provider split:\n%s", got)
	}
	// One email whose subject changed is a change, not lost plus new.
	renamed := account
	renamed.Subject = "Your Shop account"
	got = titles(Messages([]model.Message{account}, []model.Message{renamed}))
	if got != "changed: Email to the test submitter: subject changed" {
		t.Errorf("subject change:\n%s", got)
	}
	// The same email sent fewer times.
	got = titles(Messages([]model.Message{account, account}, []model.Message{account}))
	if !strings.Contains(got, "is sent fewer times") || strings.Contains(got, "broke") {
		t.Errorf("fewer:\n%s", got)
	}
}

// TestMaskedHookURLsPair: catch hook URLs are stored with their secret
// masked; the masked form is stable, so the same hook pairs across runs.
func TestMaskedHookURLsPair(t *testing.T) {
	hook := func(url, body string) model.Message {
		return model.Message{Channel: model.Webhook, Provider: "zapier", Method: "POST", URL: url, To: []string{strings.TrimPrefix(url, "https://")},
			Status: model.Faked, Body: body}
	}
	b := []model.Message{hook("https://hooks.zapier.com/hooks/catch/123/ab•••••/", `{"order":1}`)}
	a := []model.Message{hook("https://hooks.zapier.com/hooks/catch/123/ab•••••/", `{"order":2}`)}
	if got := Messages(b, a); len(got) != 0 {
		t.Errorf("the same masked hook changed:\n%s", titles(got))
	}
	a = []model.Message{hook("https://hooks.zapier.com/hooks/catch/123/xy•••••/", `{"order":2}`)}
	got := titles(Messages(b, a))
	if !strings.Contains(got, "broke: Zapier webhook to hooks.zapier.com/hooks/catch/123/ab••••• is no longer sent") ||
		!strings.Contains(got, "new: Zapier webhook to hooks.zapier.com/hooks/catch/123/xy••••• is now sent") {
		t.Errorf("another hook is another message:\n%s", got)
	}
}

func TestNewActionsAreReported(t *testing.T) {
	old := model.Action{ID: "a", Label: "Contact form", Messages: []model.Message{email("admin@shop.com", "New lead", "x")}}
	added := model.Action{ID: "b", Label: "Newsletter form", Messages: []model.Message{email("admin@shop.com", "New subscriber", "y")}}
	c := Compare(run(old), run(old, added), "", "", "test")
	got := titles(c.Changes)
	if !strings.Contains(got, "new: Email to admin@shop.com is now sent") || len(c.Changes[0].Actions) != 1 || c.Changes[0].Actions[0] != "Newsletter form" {
		t.Errorf("a new action's messages are new:\n%s", got)
	}
	if strings.Join(c.Warnings, "\n") != "1 action isn't in the snapshot; what it sent is listed as new" {
		t.Errorf("warnings = %q", c.Warnings)
	}
	c = Compare(run(old, added, model.Action{ID: "c"}), run(), "", "", "test")
	if strings.Join(c.Warnings, "\n") != "3 actions from the snapshot weren't run this time" {
		t.Errorf("warnings = %q", c.Warnings)
	}
	c = Compare(run(old, added), run(added), "", "", "test")
	if strings.Join(c.Warnings, "\n") != "1 action from the snapshot wasn't run this time" {
		t.Errorf("warnings = %q", c.Warnings)
	}
}

func TestSentMoreOrFewerTimes(t *testing.T) {
	m := email("admin@shop.com", "New order #1042", "x")
	more := Messages([]model.Message{m}, []model.Message{m, m})
	if got := titles(more); got != `changed: Email to admin@shop.com ("New order #") is sent more times` ||
		more[0].Before != "1 time" || more[0].After != "2 times" {
		t.Errorf("a duplicate send:\n%s %+v", got, more)
	}
	fewer := Messages([]model.Message{m, m, m}, []model.Message{m})
	if got := titles(fewer); got != `changed: Email to admin@shop.com ("New order #") is sent fewer times` ||
		fewer[0].Before != "3 times" || fewer[0].After != "1 time" {
		t.Errorf("fewer sends:\n%s %+v", got, fewer)
	}
}

func TestSubjectChangesKeepTheirCase(t *testing.T) {
	b := email("admin@shop.com", "Your Order #1042 at Shop", "x")
	a := email("admin@shop.com", "Your ORDER #1043 is ready", "x")
	cs := Messages([]model.Message{b}, []model.Message{a})
	if len(cs) != 1 || cs[0].Before != "Your Order # at Shop" || cs[0].After != "Your ORDER # is ready" {
		t.Errorf("subject change = %+v", cs)
	}
	a.Subject = "YOUR ORDER #1043 AT SHOP"
	if cs := Messages([]model.Message{b}, []model.Message{a}); len(cs) != 0 {
		t.Errorf("case alone isn't a change: %+v", cs)
	}
}

func TestMovedZapierHook(t *testing.T) {
	hook := func(path string) model.Message {
		return model.Message{Channel: model.Webhook, Provider: "zapier", Method: "POST", Status: model.Faked,
			URL: "https://hooks.zapier.com/hooks/catch/" + path + "/"}
	}
	got := titles(Messages([]model.Message{hook("1234567/ab•••••")}, []model.Message{hook("7654321/zx•••••")}))
	for _, want := range []string{
		"broke: Zapier webhook to hooks.zapier.com/hooks/catch/1234567/ab••••• is no longer sent",
		"new: Zapier webhook to hooks.zapier.com/hooks/catch/7654321/zx••••• is now sent",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}
