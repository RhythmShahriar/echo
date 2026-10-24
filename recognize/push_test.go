package recognize

import (
	"testing"

	"github.com/RhythmShahriar/echo/model"
)

var pushCases = []tcase{
	{name: "v1 send", provider: "fcm", status: 200,
		req: jsonReq("POST", "https://fcm.googleapis.com/v1/projects/shop-app/messages:send", `{"message":{"token":"dGVzdC10b2tlbg","notification":{"title":"Order shipped","body":"Arrives Friday"}}}`),
		check: func(t *testing.T, m model.Message) {
			eq(t, "channel", m.Channel, model.Push)
			eqList(t, "to", m.To, "dGVzdC10b2tlbg")
			eq(t, "subject", m.Subject, "Order shipped")
			eq(t, "text", m.Text, "Arrives Friday")
		}},
	{name: "notification", provider: "onesignal", status: 200,
		req: jsonReq("POST", "https://api.onesignal.com/notifications", `{"app_id":"5eb5a37e-b458-11e3-ac11-000c2940e62c","included_segments":["Subscribed Users"],
			"headings":{"en":"Sale"},"contents":{"en":"20% off today"}}`),
		check: func(t *testing.T, m model.Message) {
			eqList(t, "to", m.To, "segment Subscribed Users")
			eq(t, "text", m.Text, "20% off today")
		}},
}

var pushNegatives = []negative{
	{"FCM legacy topics API", "fcm", jsonReq("POST", "https://fcm.googleapis.com/v1/projects/p/topics", `{}`)},
	{"OneSignal apps", "onesignal", jsonReq("GET", "https://onesignal.com/api/v1/apps", "")},
}
