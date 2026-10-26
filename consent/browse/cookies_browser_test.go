// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build browser

package browse

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
)

// snap takes a snapshot of the run or fails the test.
func snap(t *testing.T, s *session) {
	t.Helper()
	if err := s.snapshot(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// cookieEvents are a recording's cookies by name.
func cookieEvents(rec *Recording) map[string]*consent.Cookie {
	out := map[string]*consent.Cookie{}
	for _, e := range kinds(rec, consent.EvCookie) {
		out[e.Cookie.Name] = e.Cookie
	}
	return out
}

// TestConsentCookieValuesNeverStored: a page sets _ga by script and a
// response sets another cookie; the run records both by name and
// length. No file the run writes holds either value.
func TestConsentCookieValuesNeverStored(t *testing.T) {
	ga := "GA1.1." + strings.Repeat("31", 8) // assembled at runtime
	sid := "s" + strings.Repeat("e", 9) + "cret"
	st := newSite(t)
	st.page("shop.test/", `<!doctype html><title>c</title><script>document.cookie = "_ga=`+ga+`; path=/; max-age=600";</script>`,
		"Set-Cookie", "sid="+sid+"; Path=/; HttpOnly")
	p := consenttest.NewPlan(t, "https://shop.test/")
	p.MapHosts = map[string]string{"shop.test": st.addr()}
	g := newRig(t, p)
	s := g.open("https://shop.test/", consent.RunNone)
	g.load(s, "https://shop.test/")
	snap(t, s)
	rec := s.close()
	c := cookieEvents(rec)
	if c["_ga"] == nil || c["_ga"].Length != len(ga) || c["_ga"].SetBy != "script" || c["_ga"].Tracker != "ga4" {
		t.Errorf("_ga %+v", c["_ga"])
	}
	if c["sid"] == nil || !c["sid"].HTTPOnly || c["sid"].SetBy != "response:shop.test" {
		t.Errorf("sid %+v", c["sid"])
	}
	out, _ := json.Marshal(rec)
	if strings.Contains(string(out), ga) || strings.Contains(string(out), sid) {
		t.Error("the recording holds a cookie value")
	}
	_ = filepath.WalkDir(p.Dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if b, _ := os.ReadFile(path); strings.Contains(string(b), ga) || strings.Contains(string(b), sid) {
			t.Errorf("%s holds a cookie value", path)
		}
		return nil
	})
}

// TestHttpOnlyCookieSeen: an HttpOnly cookie a response sets (FPID, the
// server-side GA cookie) is in the snapshot, attributed to the response.
func TestHttpOnlyCookieSeen(t *testing.T) {
	st := newSite(t)
	st.page("127.0.0.1/", `<!doctype html><title>h</title>`, "Set-Cookie", "FPID=FPID2.2."+strings.Repeat("7", 10)+"; Path=/; HttpOnly; Max-Age=600")
	g := newRig(t, consenttest.NewPlan(t, st.srv.URL+"/"))
	s := g.open(st.srv.URL+"/", consent.RunNone)
	g.load(s, st.srv.URL+"/")
	snap(t, s)
	c := cookieEvents(s.close())["FPID"]
	if c == nil || !c.HTTPOnly || c.SetBy != "response:127.0.0.1" || c.Domain != "127.0.0.1" {
		t.Errorf("FPID %+v", c)
	}
}

// TestBlockedCookieRecorded: a third party's cookie Chrome refuses to
// store is recorded with Chrome's reason: the tracker tried.
func TestBlockedCookieRecorded(t *testing.T) {
	third := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("Set-Cookie", "_fbp=fb.1.2; Path=/; SameSite=None")
		w.Header().Set("Content-Type", "image/gif")
		_, _ = w.Write(heldGIF)
	}))
	t.Cleanup(third.Close)
	port := third.URL[strings.LastIndex(third.URL, ":")+1:]
	st := newSite(t)
	st.page("127.0.0.1/", `<!doctype html><title>b</title><img id="px" src="http://localhost:`+port+`/px.gif">`)
	g := newRig(t, consenttest.NewPlan(t, st.srv.URL+"/"))
	s := g.open(st.srv.URL+"/", consent.RunNone)
	g.load(s, st.srv.URL+"/")
	g.waitFor(s, `document.getElementById("px").complete`)
	snap(t, s)
	c := cookieEvents(s.close())["_fbp"]
	if c == nil || c.Blocked == "" || c.SetBy != "response:localhost" || c.Tracker != "meta-pixel" {
		t.Errorf("_fbp %+v", c)
	}
}

// TestStorageKeys: a Clarity-like _clck in localStorage before the
// choice is a storage event in phase before, with the value's length;
// session storage and IndexedDB names are listed too, a frame's origin
// separately.
func TestStorageKeys(t *testing.T) {
	st := newSite(t)
	st.page("shop.test/", `<!doctype html><title>s</title><script>
localStorage.setItem("_clck", "abc|2|f00|0|1");
sessionStorage.setItem("cart", "3");
const req = indexedDB.open("firebaseLocalStorageDb"); req.onsuccess = () => { window.idb = true; };
</script><iframe src="https://widget.other.test/w.html"></iframe>`)
	st.page("widget.other.test/w.html", `<script>localStorage.setItem("w_id", "12345");</script>`)
	p := consenttest.NewPlan(t, "https://shop.test/")
	p.MapHosts = map[string]string{"shop.test": st.addr(), "widget.other.test": st.addr()}
	g := newRig(t, p)
	consenttest.Use(t, consent.Stages{Classifier: &storageClassifier{}})
	s := g.open("https://shop.test/", consent.RunReject)
	g.load(s, "https://shop.test/")
	g.waitFor(s, `window.idb === true`)
	snap(t, s)
	s.rec.setChoice(s.rec.now(), consent.PhaseAfterReject)
	snap(t, s) // nothing changed: nothing new
	got := map[string]*consent.StorageKey{}
	for _, e := range kinds(s.close(), consent.EvStorage) {
		if _, dup := got[e.Storage.Key]; dup {
			t.Errorf("%s recorded twice", e.Storage.Key)
		}
		got[e.Storage.Key] = e.Storage
	}
	if k := got["_clck"]; k == nil || k.Area != "local" || k.Origin != "https://shop.test" || k.Length != 13 || k.Phase != consent.PhaseBefore || k.Tracker != "clarity" {
		t.Errorf("_clck %+v", k)
	}
	if k := got["cart"]; k == nil || k.Area != "session" {
		t.Errorf("cart %+v", k)
	}
	if k := got["firebaseLocalStorageDb"]; k == nil || k.Area != "indexeddb" {
		t.Errorf("indexeddb %+v", k)
	}
	if k := got["w_id"]; k == nil || k.Origin != "https://widget.other.test" {
		t.Errorf("frame key %+v", k)
	}
}
