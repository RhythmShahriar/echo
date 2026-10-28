## ♪ Echo consent

**Findings.** 2 pages, 6 runs, 2 trackers.

### Errors (1)

| Check | Page | Run | What |
|---|---|---|---|
| consent/before-choice | /?token=•••••&api_key=•••••&q=hello | No choice | Meta Pixel fired before the visitor chose |

### Timeline: No choice on https://shop.test/?token=•••••&api_key=•••••&q=hello

| Time | Event |
|---|---|
| 0.84s | hit www.google-analytics.com/g/collect?v=2&tid=••••D001&gcs=G100 |
| 0.91s | cookie _ga_••••D001 written |
| 1.02s | hit www.facebook.com/tr?id=••••5678&ev=PageView |
