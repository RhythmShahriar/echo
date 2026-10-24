package check

import (
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/model"
)

func titles(is []model.Issue) string {
	var t []string
	for _, i := range is {
		t = append(t, i.Check+": "+i.Title)
	}
	return strings.Join(t, "\n")
}

func TestEmailContent(t *testing.T) {
	m := model.Message{ID: "m1", Channel: model.Email, Status: model.Faked, From: "wordpress@shop.com", To: []string{"buyer@example.com"},
		Subject: "Thanks {first_name}", HTML: `<p>Order ready</p><br /><b>Warning</b>:  Undefined array key "promo" in <b>/var/www/html/wp-content/plugins/promo/promo.php</b> on line <b>88</b><br /><a href="http://localhost:8080/account">Account</a> Itâ€™s here`}
	got := titles(Message(m, Options{SiteHost: "shop.com"}))
	for _, want := range []string{
		"content/placeholder: Unfilled placeholder {first_name} in email",
		"content/php-error: PHP error printed in email",
		"content/encoding: Garbled characters in email",
		"content/dev-link: Email links to a development address (localhost)",
		"email/no-text: Email has no plain-text part",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "from-domain") {
		t.Error("the site's own domain isn't another domain")
	}
}

func TestCleanEmailHasNoIssues(t *testing.T) {
	m := model.Message{Channel: model.Email, Status: model.Faked, From: "orders@mail.shop.com", To: []string{"buyer@gmail.com"},
		Subject: "Your order", Text: "Thanks for your order. Track it at https://shop.com/track", HTML: "<p>Thanks</p>"}
	if is := Message(m, Options{SiteHost: "www.shop.com"}); len(is) != 0 {
		t.Errorf("unexpected issues:\n%s", titles(is))
	}
}

func TestSMS(t *testing.T) {
	long := strings.Repeat("Your order has shipped. ", 8) // 192 GSM-7 chars
	m := model.Message{Channel: model.SMS, Provider: "twilio", Status: model.Faked, To: []string{"015112345678"}, Text: long}
	got := titles(Message(m, Options{}))
	if !strings.Contains(got, "sms/number") || !strings.Contains(got, "SMS needs more than one part") {
		t.Errorf("got:\n%s", got)
	}
	if enc, n := segments("Hi 👋"); enc != "UCS-2" || n != 1 {
		t.Errorf("emoji: %s %d", enc, n)
	}
	if _, n := segments(strings.Repeat("é", 71)); n != 1 {
		t.Errorf("é is GSM-7: %d", n)
	}
	if _, n := segments(strings.Repeat("ł", 71)); n != 2 {
		t.Errorf("ł forces UCS-2: %d", n)
	}
}

func TestFailuresAndWebhooks(t *testing.T) {
	m := model.Message{Channel: model.Webhook, URL: "https://hooks.zapier.com/hooks/catch/1/abc/", Status: model.Failed,
		Headers: map[string]string{"content-type": "application/json"}, Body: `{"order":`, Response: &model.Response{Status: 500}}
	got := titles(Message(m, Options{}))
	for _, want := range []string{"send/failed: Webhook to hooks.zapier.com fails (HTTP 500)", "webhook/json: Webhook to hooks.zapier.com sends invalid JSON"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestRealRecipientsInObserveMode(t *testing.T) {
	m := model.Message{Channel: model.Email, Status: model.Sent, Subject: "x", Text: "x", To: []string{"jane@gmail.com"}}
	if !strings.Contains(titles(Message(m, Options{})), "seal/real-recipient") {
		t.Error("a real recipient in observe mode must be flagged")
	}
	if strings.Contains(titles(Message(m, Options{AllowRecipients: []string{"*@gmail.com"}})), "seal/real-recipient") {
		t.Error("allowlisted recipients are fine")
	}
	m.To = []string{"soundcheck-test+1@example.com"}
	if strings.Contains(titles(Message(m, Options{})), "seal/real-recipient") {
		t.Error("example.com is a test address")
	}
}

func TestPlaceholdersIgnoreJSONAndCSS(t *testing.T) {
	m := model.Message{Channel: model.Email, Status: model.Faked, Subject: "Hi", Text: "x",
		HTML: `<style>a{color:red}</style><p>{"a":1}</p>`}
	if got := titles(Message(m, Options{})); strings.Contains(got, "placeholder") {
		t.Errorf("false placeholder:\n%s", got)
	}
}

func TestSecretInPayloadWhateverTheStatus(t *testing.T) {
	for _, st := range []model.Status{model.Faked, model.Sent, model.Blocked, model.Failed} {
		m := model.Message{Channel: model.Webhook, URL: "https://crm.shop.test/api/leads", Status: st,
			Body: `{"email":"a@b.test","api_key":"ab•••••"}`}
		if !strings.Contains(titles(Message(m, Options{})), "content/secret") {
			t.Errorf("%s: a credential in the payload must be flagged", st)
		}
	}
	m := model.Message{Channel: model.Webhook, URL: "https://crm.shop.test/api/leads", Status: model.Faked, Body: `{"email":"a@b.test"}`}
	if strings.Contains(titles(Message(m, Options{})), "content/secret") {
		t.Error("no credential, no issue")
	}
}
