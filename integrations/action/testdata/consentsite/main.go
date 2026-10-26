// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Command consentsite serves the two pages the Action's consent test
// checks, on 127.0.0.1:18990 (-addr to change it):
//
//	/good/    a generic cookie banner and a stand-in gtag that sets the
//	          Consent Mode default to denied and updates it on the
//	          choice; the stand-in pixel fires only after Accept
//	/broken/  the same page with the pixel firing on every load: before
//	          the choice and after Reject
//	/tr       the stand-in pixel (Meta's /tr on the site's own host),
//	          counting every hit that reaches it
//	/hits     that count: 0 when Echo held every hit in the browser
//
// The pages load nothing from another host.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// page is both pages; FIRE is true on the broken one.
const page = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Consent test shop</title>
<script>
window.dataLayer = window.dataLayer || [];
function gtag() { dataLayer.push(arguments); }
var signals = ["ad_storage", "analytics_storage", "ad_user_data", "ad_personalization"];
function state(value) {
  var s = {};
  signals.forEach(function (k) { s[k] = value; });
  return s;
}
var defaults = state("denied");
defaults.wait_for_update = 500;
gtag("consent", "default", defaults);
var fireAlways = FIRE;
function pixel() { new Image().src = "/tr?id=1234567890&ev=PageView"; }
function stored() {
  var m = document.cookie.match(/(?:^|; )shop_consent=(accepted|rejected)/);
  return m ? m[1] : "";
}
function apply(choice) {
  gtag("consent", "update", state(choice === "accepted" ? "granted" : "denied"));
  var banner = document.getElementById("cookie-banner");
  if (banner) { banner.remove(); }
  if (choice === "accepted" && !fireAlways) { pixel(); }
}
function choose(choice) {
  document.cookie = "shop_consent=" + choice + "; path=/; max-age=31536000; SameSite=Lax";
  apply(choice);
}
</script>
</head>
<body>
<main>
<h1>Consent test shop</h1>
<p>A page for the Echo Action's consent test.</p>
<p><a href="PREFIXabout">About the shop</a></p>
</main>
<div id="cookie-banner" role="dialog" aria-label="Cookie consent"
  style="position: fixed; left: 0; right: 0; bottom: 0; z-index: 9999; padding: 16px; background: #fff; border-top: 1px solid #888">
<p>We use cookies for analytics and advertising. You can accept or reject them.</p>
<button id="accept" type="button" onclick="choose('accepted')">Accept all</button>
<button id="reject" type="button" onclick="choose('rejected')">Reject all</button>
</div>
<script>
if (fireAlways) { pixel(); }
if (stored()) { apply(stored()); }
</script>
</body>
</html>
`

// gif is a 1x1 transparent GIF, the pixel's answer.
var gif = []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\x00\x00\x00!\xf9\x04\x01\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;")

// render is the page under prefix (/good/ or /broken/).
func render(prefix string, fire bool) string {
	return strings.NewReplacer("FIRE", fmt.Sprint(fire), "PREFIX", prefix).Replace(page)
}

// newHandler serves the pages and counts the pixel's hits in hits.
func newHandler(hits *atomic.Int64) http.Handler {
	mux := http.NewServeMux()
	for prefix, fire := range map[string]bool{"/good/": false, "/broken/": true} {
		body := render(prefix, fire)
		mux.HandleFunc(prefix, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = fmt.Fprint(w, body)
		})
	}
	mux.HandleFunc("/tr", func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/gif")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(gif)
	})
	mux.HandleFunc("/hits", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = fmt.Fprintln(w, hits.Load())
	})
	return mux
}

func main() {
	addr := flag.String("addr", "127.0.0.1:18990", "address to listen on (loopback only)")
	flag.Parse()
	if !strings.HasPrefix(*addr, "127.0.0.1:") {
		log.Fatalf("consentsite: -addr %s: listen on 127.0.0.1 only", *addr)
	}
	var hits atomic.Int64
	srv := &http.Server{Addr: *addr, Handler: newHandler(&hits), ReadHeaderTimeout: 5 * time.Second}
	log.Printf("consentsite: listening on http://%s/good/ and /broken/", *addr)
	log.Fatal(srv.ListenAndServe())
}
