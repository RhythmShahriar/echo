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
