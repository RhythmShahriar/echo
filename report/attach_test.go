package report

import (
	"bytes"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/model"
)

// withInvoice is the sample run with an invoice attached to the first
// email, as the SMTP sink stores it.
func withInvoice() *model.Run {
	r := comparedRun()
	r.Actions[0].Messages[0].Body = "From: wordpress@shop.test\r\nTo: admin@shop.test\r\nContent-Type: multipart/mixed; boundary=b\r\n\r\n" +
		"--b\r\nContent-Type: text/html\r\n\r\n<p>Hi</p>\r\n" +
		"--b\r\nContent-Type: application/pdf; name=\"invoice-42.pdf\"\r\nContent-Disposition: attachment\r\nContent-Transfer-Encoding: 8bit\r\n\r\n" +
		"[attachment invoice-42.pdf application/pdf 84211 sha256:" + strings.Repeat("0f", 32) + "]\r\n--b--\r\n"
	return r
}

const invoiceLine = "invoice-42.pdf (application/pdf, 84 KB)"

func TestAttachmentsAreShown(t *testing.T) {
	for _, attached := range []bool{true, false} {
		run := comparedRun()
		if attached {
			run = withInvoice()
		}
		outputs := map[string]string{}
		for _, plain := range []bool{true, false} {
			var buf bytes.Buffer
			NewPretty(&buf, &buf, PrettyOptions{Plain: plain}).Message(run.Actions[0].Messages[0])
			outputs["pretty"] += buf.String()
		}
		var md, html bytes.Buffer
		if err := Markdown(&md, Result{Run: run}); err != nil {
			t.Fatal(err)
		}
		if err := HTML(&html, Result{Run: run}); err != nil {
			t.Fatal(err)
		}
		outputs["markdown"], outputs["html"] = md.String(), html.String()
		for name, out := range outputs {
			if got := strings.Contains(out, invoiceLine); got != attached {
				t.Errorf("%s, attached %v: shown %v:\n%s", name, attached, got, out)
			}
		}
		if attached && !strings.Contains(md.String(), "| Email to admin@shop.test | Contact form | "+invoiceLine+" |") {
			t.Errorf("markdown table:\n%s", md.String())
		}
	}
}
