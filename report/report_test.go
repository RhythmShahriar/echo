package report

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/model"
)

func issue(level model.Level, title string) model.Issue {
	i := model.Issue{Check: "content/x", Level: level, Title: title, Fix: "Do this."}
	i.Identify()
	return i
}

func sampleRun() *model.Run {
	at := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	return &model.Run{Schema: model.Schema, Tool: "rhythm-echo", Version: "1.0.0", Target: "https://staging.shop.test", Mode: "seal",
		Started: at, Duration: 4200,
		Actions: []model.Action{
			{ID: "action:contact", Label: "Contact form", Kind: "http", Messages: []model.Message{
				{ID: "m1", Channel: model.Email, Status: model.Faked, From: "wordpress@shop.test", To: []string{"admin@shop.test"}, Subject: "New message",
					HTML: `<p>Hi</p><script>alert(1)</script>`, At: at},
				{ID: "m2", Channel: model.SMS, Provider: "twilio", Status: model.Faked, To: []string{"+15551230000"}, Text: "Thanks {first_name}", At: at},
				{ID: "m3", Channel: model.Webhook, Status: model.Failed, Method: "POST", URL: "https://hooks.zapier.com/hooks/catch/1/x/",
					Body: `{"a":1}`, Response: &model.Response{Status: 500, Body: "oops"}, At: at},
			}, Issues: []model.Issue{issue(model.Error, "Unfilled placeholder {first_name} in Twilio SMS")}},
			{ID: "action:order", Label: "Completed order", Kind: "command", Error: "exit status 1"},
		},
	}
}

func sampleComparison() *model.Comparison {
	return &model.Comparison{Schema: model.Schema, Version: "1.0.0", Actions: 2,
		Before: model.RunRef{Name: "before-update", Started: time.Now().Add(-2 * time.Hour)},
		Changes: []model.Change{
			{Class: model.Broke, Check: "message/missing", Title: "Email to admin@shop.test is no longer sent", Actions: []string{"Contact form", "Quote form"}, Before: "1", After: "0", Fix: "Check the mail plugin."},
			{Class: model.New, Check: "message/new", Title: "Slack message is now sent", Actions: []string{"Completed order"}},
			{Class: model.Changed, Check: "message/subject", Title: "Email to the test submitter: subject changed", Actions: []string{"Contact form"}, Before: "Thanks", After: "Thank you"},
			{Class: model.Fixed, Check: "content/x", Title: "PHP error printed in email", Actions: []string{"Contact form"}, Fix: "never shown"},
		},
		Warnings: []string{"1 action from the snapshot wasn't run this time"}}
}

func TestFailOnAndTally(t *testing.T) {
	run := sampleRun()
	c := Result{Run: run, Comparison: sampleComparison()}
	if !FailOn(c, "broke") || !FailOn(c, "changed") || FailOn(c, "") {
		t.Error("FailOn for comparisons")
	}
	c.Comparison.Changes = c.Comparison.Changes[3:]
	if FailOn(c, "changed") {
		t.Error("FIXED never fails")
	}
	if !FailOn(Result{Run: run}, "error") {
		t.Error("a failed action fails --fail-on error")
	}
	if got := Tally(run); got != "1 email, 1 SMS, 1 webhook" {
		t.Errorf("Tally = %q", got)
	}
	if ValidFailOn("fixed", true) == nil || ValidFailOn("notice", false) != nil {
		t.Error("ValidFailOn")
	}
}

func TestPlainOutputs(t *testing.T) {
	var buf bytes.Buffer
	p := NewPretty(&buf, &buf, PrettyOptions{Plain: true})
	p.Summary(Result{Run: sampleRun(), Comparison: sampleComparison()}, "report: x.html")
	out := buf.String()
	for _, want := range []string{"[BROKE] Email to admin@shop.test is no longer sent - Contact form, Quote form", "1 → 0",
		"fix: Check the mail plugin.", "[FIXED] PHP error printed in email", "Something broke - 1 broke, 1 new, 1 changed, 1 fixed",
		"note: 1 action from the snapshot"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "never shown") || strings.Contains(out, "\x1b[") {
		t.Errorf("plain output:\n%s", out)
	}
	buf.Reset()
	p.Summary(Result{Run: sampleRun()}, "")
	if out := buf.String(); !strings.Contains(out, "1 action failed") || !strings.Contains(out, "[ERROR] Unfilled placeholder") {
		t.Errorf("run summary:\n%s", out)
	}
	buf.Reset()
	p.Message(sampleRun().Actions[0].Messages[1])
	if got := buf.String(); !strings.Contains(got, "Twilio SMS to +15551230000: Thanks {first_name} [faked]") {
		t.Errorf("message line = %q", got)
	}
	buf.Reset()
	p.Message(model.Message{Channel: model.Webhook, Status: model.Faked, Method: "POST", URL: "https://crm.shop.test/api/leads",
		To: []string{"crm.shop.test/api/leads"}})
	if got := buf.String(); !strings.Contains(got, " webhook to crm.shop.test/api/leads: POST [faked]") || strings.Count(got, "crm.shop.test") != 1 {
		t.Errorf("the host is shown once: %q", got)
	}
}

func TestPrettyRendersWithoutPanics(t *testing.T) {
	var buf bytes.Buffer
	p := NewPretty(&buf, &buf, PrettyOptions{Verbose: true})
	p.Header("1.0.0", "https://staging.shop.test")
	r := sampleRun()
	for i := range r.Actions {
		p.Action(&r.Actions[i], i+1, len(r.Actions))
	}
	p.Summary(Result{Run: r, Comparison: sampleComparison()}, "hint")
	p.Summary(Result{Run: r}, "")
	if !strings.Contains(buf.String(), "ECHO") {
		t.Error("no panel")
	}
}

func TestMachineFormats(t *testing.T) {
	r := Result{Run: sampleRun(), Comparison: sampleComparison()}
	var buf bytes.Buffer
	if err := JSON(&buf, r); err != nil {
		t.Fatal(err)
	}
	var c model.Comparison
	if json.Unmarshal(buf.Bytes(), &c) != nil || len(c.Changes) != 4 {
		t.Error("JSON round trip")
	}
	buf.Reset()
	if err := Markdown(&buf, r); err != nil {
		t.Fatal(err)
	}
	if md := buf.String(); !strings.Contains(md, "**Something broke.**") || !strings.Contains(md, "| 1 | 1 | 1 | 1 |") {
		t.Errorf("markdown:\n%s", md)
	}
	buf.Reset()
	if err := JUnit(&buf, r, ""); err != nil {
		t.Fatal(err)
	}
	var s junitSuites
	if xml.Unmarshal(buf.Bytes(), &s) != nil || s.Failures != 2 || s.Tests != 4 {
		t.Errorf("junit: %d failures of %d", s.Failures, s.Tests)
	}
	buf.Reset()
	_ = JUnit(&buf, Result{Run: sampleRun()}, "")
	_ = xml.Unmarshal(buf.Bytes(), &s)
	if s.Failures != 2 { // the failed action and the error issue
		t.Errorf("run junit failures = %d", s.Failures)
	}
}

func TestHTMLIsSafeAndComplete(t *testing.T) {
	var buf bytes.Buffer
	if err := HTML(&buf, Result{Run: sampleRun(), Comparison: sampleComparison()}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"Something broke", "Email to admin@shop.test is no longer sent", `<iframe sandbox`,
		"Thanks {first_name}", "&#34;a&#34;: 1", "500 oops", "The action failed: exit status 1"} {
		if !strings.Contains(out, want) {
			t.Errorf("html lacks %q", want)
		}
	}
	if strings.Contains(out, "<script>alert(1)") {
		t.Error("email HTML must be escaped into srcdoc, never inlined")
	}
	if strings.Contains(out, "ZgotmplZ") {
		t.Error("a value was rejected by html/template")
	}
}
