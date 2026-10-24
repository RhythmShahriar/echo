## ♪ Echo

**Snapshot "ci" saved; 1 action failed.** 2 actions run; 2 emails, 1 SMS sent.

| Action | Sent | Issues |
|---|---|---:|
| Order form | 2 emails, 1 SMS | 2 |
| Contact form | failed: POST http://127.0.0.1:20519/contact: connection refused | 1 |

| Level | Issue | Actions |
|---|---|---|
| error | Unfilled placeholder {first_name} in email to ***@customer.example | Order form |
| warning | Email really sent to ***@real.example | site-wide |
| notice | Email has no plain-text part | Order form, Contact form |

<sub>Recipients, subjects and message contents are hidden here. Set `summary-details: true` to show them, or open the HTML report.</sub>
