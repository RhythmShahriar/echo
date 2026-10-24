// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package recognize

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/model"
)

var (
	sendgridKey = join("S", "G.", "Zx81kQpLmN0aBcDeFgHiJk", ".", "QrStUvWxYz0123456789AbCdEfGhIjKlMnOp")
	mailgunAuth = "Basic " + base64.StdEncoding.EncodeToString([]byte(join("api:", "ke", "y-", "0123456789abcdef0123456789abcdef")))
)

func emailIs(t *testing.T, m model.Message, from, subject string, to ...string) {
	t.Helper()
	eq(t, "channel", m.Channel, model.Email)
	eq(t, "from", m.From, from)
	eq(t, "subject", m.Subject, subject)
	eqList(t, "to", m.To, to...)
}

var mailgunMultipart = strings.ReplaceAll(`--b1
Content-Disposition: form-data; name="from"

Shop <orders@mg.shop.test>
--b1
Content-Disposition: form-data; name="to"

ann@example.com
--b1
Content-Disposition: form-data; name="to"

bob@example.com
--b1
Content-Disposition: form-data; name="subject"

Order #1042 confirmed
--b1
Content-Disposition: form-data; name="html"

<p>Thanks, Ann</p>
--b1
Content-Disposition: form-data; name="attachment"; filename="invoice-1042.pdf"
Content-Type: application/pdf

%PDF-1.4 fake
--b1--
`, "\n", "\r\n")

var emailCases = []tcase{
	{name: "mail send", provider: "sendgrid", status: 202,
		req: jsonReq("POST", "https://api.sendgrid.com/v3/mail/send", `{"personalizations":[{"to":[{"email":"ann@example.com","name":"Ann"}],"cc":[{"email":"ops@shop.test"}]}],
			"from":{"email":"orders@shop.test","name":"Shop"},"reply_to":{"email":"help@shop.test"},"subject":"Your order #1042",
			"content":[{"type":"text/plain","value":"Thanks Ann"},{"type":"text/html","value":"<p>Thanks Ann</p>"}]}`,
			"authorization", "Bearer "+sendgridKey),
		check: func(t *testing.T, m model.Message) {
			emailIs(t, m, "orders@shop.test", "Your order #1042", "ann@example.com")
			eqList(t, "cc", m.CC, "ops@shop.test")
			eq(t, "reply-to", m.ReplyTo, "help@shop.test")
			eq(t, "text", m.Text, "Thanks Ann")
			eq(t, "html", m.HTML, "<p>Thanks Ann</p>")
		}},
	{name: "form messages", provider: "mailgun", status: 200,
		req: formReq("https://api.mailgun.net/v3/mg.shop.test/messages",
			"from=Shop+%3Corders%40mg.shop.test%3E&to=ann%40example.com&bcc=audit%40shop.test&subject=Welcome&text=Hello+Ann&h%3AReply-To=help%40shop.test",
			"authorization", mailgunAuth),
		check: func(t *testing.T, m model.Message) {
			emailIs(t, m, "Shop <orders@mg.shop.test>", "Welcome", "ann@example.com")
			eqList(t, "bcc", m.BCC, "audit@shop.test")
			eq(t, "reply-to", m.ReplyTo, "help@shop.test")
		}},
	{name: "multipart from the PHP SDK", provider: "mailgun", status: 200,
		req: req("POST", "https://api.eu.mailgun.net/v3/mg.shop.test/messages",
			map[string]string{"content-type": "multipart/form-data; boundary=b1"}, mailgunMultipart),
		check: func(t *testing.T, m model.Message) {
			emailIs(t, m, "Shop <orders@mg.shop.test>", "Order #1042 confirmed", "ann@example.com", "bob@example.com")
			eq(t, "html", m.HTML, "<p>Thanks, Ann</p>")
		}},
	{name: "single email", provider: "postmark", status: 200,
		req: jsonReq("POST", "https://api.postmarkapp.com/email", `{"From":"orders@shop.test","To":"ann@example.com, bob@example.com","Cc":"ops@shop.test",
			"Subject":"Receipt","TextBody":"Paid","HtmlBody":"<b>Paid</b>","MessageStream":"outbound"}`, "x-postmark-server-token", "0d1f2a3b-0000-4000-8000-000000000000"),
		check: func(t *testing.T, m model.Message) {
			emailIs(t, m, "orders@shop.test", "Receipt", "ann@example.com", "bob@example.com")
			eqList(t, "cc", m.CC, "ops@shop.test")
			eq(t, "html", m.HTML, "<b>Paid</b>")
		}},
	{name: "batch", provider: "postmark", status: 200,
		req: jsonReq("POST", "https://api.postmarkapp.com/email/batch", `[{"From":"a@shop.test","To":"ann@example.com","Subject":"One","TextBody":"1"},
			{"From":"a@shop.test","To":"bob@example.com","Subject":"Two","TextBody":"2"}]`),
		check: func(t *testing.T, m model.Message) {
			emailIs(t, m, "a@shop.test", "One", "ann@example.com", "bob@example.com")
		}},
	{name: "emails", provider: "resend", status: 200,
		req: jsonReq("POST", "https://api.resend.com/emails", `{"from":"Shop <hi@shop.test>","to":["ann@example.com"],"bcc":"audit@shop.test",
			"reply_to":"help@shop.test","subject":"Hello","html":"<p>Hi</p>","text":"Hi"}`, "authorization", "Bearer "+join("r", "e_", "AbCdEfGh12345678IjKlMnOp")),
		check: func(t *testing.T, m model.Message) {
			emailIs(t, m, "Shop <hi@shop.test>", "Hello", "ann@example.com")
			eqList(t, "bcc", m.BCC, "audit@shop.test")
			eq(t, "reply-to", m.ReplyTo, "help@shop.test")
		}},
	{name: "batch", provider: "resend", status: 200,
		req: jsonReq("POST", "https://api.resend.com/emails/batch", `[{"from":"hi@shop.test","to":"ann@example.com","subject":"A"},{"from":"hi@shop.test","to":"bob@example.com","subject":"B"}]`),
		check: func(t *testing.T, m model.Message) {
			emailIs(t, m, "hi@shop.test", "A", "ann@example.com", "bob@example.com")
		}},
	{name: "v2 SendEmail", provider: "amazon-ses", status: 200,
		req: jsonReq("POST", "https://email.eu-west-1.amazonaws.com/v2/email/outbound-emails", `{"FromEmailAddress":"orders@shop.test",
			"Destination":{"ToAddresses":["ann@example.com"],"CcAddresses":["ops@shop.test"]},"ReplyToAddresses":["help@shop.test"],
			"Content":{"Simple":{"Subject":{"Data":"Shipped"},"Body":{"Text":{"Data":"On its way"},"Html":{"Data":"<p>On its way</p>"}}}}}`),
		check: func(t *testing.T, m model.Message) {
			emailIs(t, m, "orders@shop.test", "Shipped", "ann@example.com")
			eqList(t, "cc", m.CC, "ops@shop.test")
			eq(t, "text", m.Text, "On its way")
			eq(t, "html", m.HTML, "<p>On its way</p>")
		}},
	{name: "v1 SendEmail form", provider: "amazon-ses", status: 200,
		req: formReq("https://email.us-east-1.amazonaws.com/", "Action=SendEmail&Source=orders%40shop.test&Destination.ToAddresses.member.1=ann%40example.com"+
			"&Destination.ToAddresses.member.2=bob%40example.com&Message.Subject.Data=Shipped&Message.Body.Text.Data=On+its+way&Version=2010-12-01"),
		check: func(t *testing.T, m model.Message) {
			emailIs(t, m, "orders@shop.test", "Shipped", "ann@example.com", "bob@example.com")
			eq(t, "text", m.Text, "On its way")
		}},
	{name: "smtp email", provider: "brevo", status: 201,
		req: jsonReq("POST", "https://api.brevo.com/v3/smtp/email", `{"sender":{"email":"news@shop.test","name":"Shop"},"to":[{"email":"ann@example.com"}],
			"subject":"Hi","htmlContent":"<p>Hi</p>","replyTo":{"email":"help@shop.test"}}`, "api-key", join("xkey", "sib-", "0123456789abcdef")),
		check: func(t *testing.T, m model.Message) {
			emailIs(t, m, "news@shop.test", "Hi", "ann@example.com")
			eq(t, "reply-to", m.ReplyTo, "help@shop.test")
		}},
	{name: "transmission", provider: "sparkpost", status: 200,
		req: jsonReq("POST", "https://api.sparkpost.com/api/v1/transmissions", `{"recipients":[{"address":{"email":"ann@example.com"}},{"address":"bob@example.com"}],
			"content":{"from":{"email":"orders@shop.test","name":"Shop"},"subject":"Receipt","text":"Paid","html":"<b>Paid</b>"}}`),
		check: func(t *testing.T, m model.Message) {
			emailIs(t, m, "orders@shop.test", "Receipt", "ann@example.com", "bob@example.com")
			eq(t, "text", m.Text, "Paid")
		}},
	{name: "messages send", provider: "mandrill", status: 200,
		req: jsonReq("POST", "https://mandrillapp.com/api/1.0/messages/send.json", `{"key":"`+join("md", "-AbCdEfGhIjKlMnOpQrSt")+`","message":{"from_email":"orders@shop.test",
			"subject":"Receipt","text":"Paid","to":[{"email":"ann@example.com","type":"to"},{"email":"audit@shop.test","type":"bcc"}],"headers":{"Reply-To":"help@shop.test"}}}`),
		check: func(t *testing.T, m model.Message) {
			emailIs(t, m, "orders@shop.test", "Receipt", "ann@example.com")
			eqList(t, "bcc", m.BCC, "audit@shop.test")
			eq(t, "reply-to", m.ReplyTo, "help@shop.test")
			if strings.Contains(m.Text+m.Subject+m.From, "md-") {
				t.Error("API key copied into the message")
			}
		}},
}

var emailNegatives = []negative{
	{"other SendGrid endpoint", "sendgrid", jsonReq("POST", "https://api.sendgrid.com/v3/marketing/contacts", `{}`)},
	{"Mailgun domains API", "mailgun", formReq("https://api.mailgun.net/v3/domains", "name=mg.shop.test")},
	{"Postmark bounces", "postmark", jsonReq("GET", "https://api.postmarkapp.com/bounces", "")},
	{"Resend domains", "resend", jsonReq("POST", "https://api.resend.com/domains", `{}`)},
	{"SES identity call", "amazon-ses", formReq("https://email.us-east-1.amazonaws.com/", "Action=VerifyEmailIdentity&EmailAddress=a%40b.test")},
	{"S3 is not SES", "amazon-ses", req("PUT", "https://s3.amazonaws.com/bucket/key", nil, "x")},
	{"Brevo contacts", "brevo", jsonReq("POST", "https://api.brevo.com/v3/contacts", `{}`)},
	{"SparkPost templates", "sparkpost", jsonReq("GET", "https://api.sparkpost.com/api/v1/templates", "")},
	{"Mandrill users", "mandrill", jsonReq("POST", "https://mandrillapp.com/api/1.0/users/ping", `{}`)},
}
