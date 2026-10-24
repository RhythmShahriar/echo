package recognize

import (
	"testing"

	"github.com/RhythmShahriar/echo/model"
)

var chatCases = []tcase{
	{name: "MessageCard webhook", provider: "teams", status: 200,
		req: jsonReq("POST", "https://contoso.webhook.office.com/webhookb2/1111/IncomingWebhook/2222/3333", `{"@type":"MessageCard","summary":"New order","title":"Order #1042","text":"Ann paid $40"}`),
		check: func(t *testing.T, m model.Message) {
			eq(t, "channel", m.Channel, model.Chat)
			eq(t, "subject", m.Subject, "Order #1042")
			eq(t, "text", m.Text, "Ann paid $40")
		}},
	{name: "Workflows adaptive card", provider: "teams", status: 202,
		req: jsonReq("POST", "https://prod-12.westus.logic.azure.com/workflows/abc/triggers/manual/paths/invoke?api-version=2016-06-01", `{"type":"message","attachments":[{"contentType":"application/vnd.microsoft.card.adaptive",
			"content":{"type":"AdaptiveCard","body":[{"type":"TextBlock","text":"Disk almost full"}]}}]}`),
		check: func(t *testing.T, m model.Message) { eq(t, "text", m.Text, "Disk almost full") }},
}

var chatNegatives = []negative{
	{"Outlook mail API", "teams", jsonReq("GET", "https://outlook.office.com/api/v2.0/me/messages", "")},
	{"Logic app without trigger", "teams", jsonReq("GET", "https://prod-12.westus.logic.azure.com/status", "")},
}
