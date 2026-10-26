package report

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
	xhtml "golang.org/x/net/html"
)

// consentHTMLOf runs WriteConsentHTML into a string.
func consentHTMLOf(t testing.TB, r *consent.Result, o ConsentHTMLOptions) string {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteConsentHTML(&buf, r, o); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// writePNG writes a w by h PNG at dir/rel.
func writePNG(t testing.TB, dir, rel string, w, h int) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// evidenceFor writes a 1280 by 800 PNG for every screenshot r names
// except those in skip, and returns the folder.
func evidenceFor(t testing.TB, r *consent.Result, skip ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, pg := range r.Pages {
		for _, run := range pg.Runs {
			for _, rel := range run.Screenshots {
				if rel != "" && !slices.Contains(skip, rel) {
					writePNG(t, dir, rel, 1280, 800)
				}
			}
		}
	}
	return dir
}

// parseHTML parses a report.
func parseHTML(t testing.TB, s string) *xhtml.Node {
	t.Helper()
	doc, err := xhtml.Parse(strings.NewReader(s))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// walk calls f for every element under n.
func walk(n *xhtml.Node, f func(*xhtml.Node)) {
	if n.Type == xhtml.ElementNode {
		f(n)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, f)
	}
}

// attr is n's attribute key, or "".
func attr(n *xhtml.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// reportCSPOf finds the CSP meta tag's policy.
func reportCSPOf(t testing.TB, doc *xhtml.Node) string {
	t.Helper()
	var csp []string
	walk(doc, func(n *xhtml.Node) {
		if n.Data == "meta" && strings.EqualFold(attr(n, "http-equiv"), "Content-Security-Policy") {
			csp = append(csp, attr(n, "content"))
		}
	})
	if len(csp) != 1 {
		t.Fatalf("%d CSP meta tags", len(csp))
	}
	return csp[0]
}

func TestConsentReportCSP(t *testing.T) {
	r := judgedResult(t, "meta-ignores")
	doc := parseHTML(t, consentHTMLOf(t, r, ConsentHTMLOptions{EvidenceDir: evidenceFor(t, r)}))
	csp := reportCSPOf(t, doc)
	want := "default-src 'none'; img-src data:; style-src 'unsafe-inline'; script-src '" + scriptHash(consentHTMLSource) +
		"'; base-uri 'none'; form-action 'none'; frame-src 'none'"
	if csp != want {
		t.Errorf("CSP\n got %s\nwant %s", csp, want)
	}
	if strings.Contains(csp, "http:") || strings.Contains(csp, "https:") || strings.Contains(csp, "*") {
		t.Errorf("CSP allows a remote source: %s", csp)
	}
	// The meta tag must come before anything that loads.
	head := consentHTMLOf(t, r, ConsentHTMLOptions{})
	if i, j := strings.Index(head, "Content-Security-Policy"), strings.Index(head, "<style>"); i < 0 || i > j {
		t.Errorf("the CSP meta comes after the style")
	}
}

func TestScriptHash(t *testing.T) {
	r := judgedResult(t, "meta-ignores")
	out := consentHTMLOf(t, r, ConsentHTMLOptions{})
	doc := parseHTML(t, out)
	var scripts []string
	walk(doc, func(n *xhtml.Node) {
		if n.Data == "script" {
			if attr(n, "src") != "" {
				t.Errorf("a script with src: %s", attr(n, "src"))
			}
			if n.FirstChild != nil {
				scripts = append(scripts, n.FirstChild.Data)
			}
		}
	})
	if len(scripts) != 1 {
		t.Fatalf("%d inline scripts, want 1", len(scripts))
	}
	sum := sha256.Sum256([]byte(scripts[0]))
	want := "script-src 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	if csp := reportCSPOf(t, doc); !strings.Contains(csp, want) {
		t.Errorf("CSP %q doesn't allow the rendered script (%s)", csp, want)
	}
	// The script only opens details and scrolls: nothing that loads or
	// reads beyond the page.
	for _, bad := range []string{"fetch", "XMLHttpRequest", "src", "eval", "innerHTML", "localStorage", "cookie", "open("} {
		if strings.Contains(scripts[0], bad) {
			t.Errorf("the script uses %s", bad)
		}
	}
}

// hostileResult is meta-ignores with every captured string pointing at
// evil.test or trying to break out of the page.
func hostileResult(t testing.TB) *consent.Result {
	t.Helper()
	r := judgedResult(t, "meta-ignores")
	for i := range r.Pages[0].Runs {
		run := &r.Pages[0].Runs[i]
		for j := range run.Hits {
			run.Hits[j].URL = "https://evil.test/collect?x=\"><img src=https://evil.test/p.gif>"
		}
		for j := range run.Scripts {
			run.Scripts[j].URL = "https://evil.test/s.js"
		}
		for j := range run.Visits {
			run.Visits[j].URL = "javascript:alert(1)//https://evil.test/"
		}
		run.Screenshots = map[string]string{"banner": "https://evil.test/shot.png", "after": "//evil.test/a.png"}
		if run.Choice != nil {
			run.Choice.Text = "<a href=https://evil.test/>Deny</a>"
			run.Choice.Selector = "a[href='https://evil.test/']"
		}
	}
	r.Pages[0].CMP.Name = `<img src="https://evil.test/cmp.png">`
	r.Issues[0].Detail = `<a href="https://evil.test/">details</a>`
	r.Issues[0].Fix = "see https://evil.test/fix"
	return r
}

func TestConsentReportNoRemoteRefs(t *testing.T) {
	out := consentHTMLOf(t, hostileResult(t), ConsentHTMLOptions{EvidenceDir: t.TempDir()})
	doc := parseHTML(t, out)
	walk(doc, func(n *xhtml.Node) {
		for _, a := range n.Attr {
			key := strings.ToLower(a.Key)
			if (key == "src" || key == "href" || key == "srcset" || key == "xlink:href" || key == "action" || key == "poster") && strings.Contains(a.Val, "evil.test") {
				t.Errorf("<%s %s=%q>", n.Data, a.Key, a.Val)
			}
			if strings.HasPrefix(key, "on") {
				t.Errorf("<%s %s=%q>", n.Data, a.Key, a.Val)
			}
		}
		if n.Data == "img" && !strings.HasPrefix(attr(n, "src"), "data:image/png;base64,") {
			t.Errorf("an image that isn't a data: PNG: %q", attr(n, "src"))
		}
	})
	if !strings.Contains(out, "evil.test/collect") {
		t.Errorf("the captured URL isn't shown as text")
	}
	if strings.Count(out, "screenshot not taken") == 0 {
		t.Errorf("a screenshot path outside the folder didn't read as not taken")
	}
}

func TestConsentReportScreenshots(t *testing.T) {
	r := judgedResult(t, "meta-ignores")
	dir := evidenceFor(t, r, "evidence/page-1/accept-after.png")
	// A PNG outside the folder that a tampered result points at.
	outside := t.TempDir()
	writePNG(t, outside, "secret.png", 3, 3)
	r.Pages[0].Runs[0].Screenshots["banner"] = "../" + filepath.Base(outside) + "/secret.png"
	out := consentHTMLOf(t, r, ConsentHTMLOptions{EvidenceDir: dir})
	doc := parseHTML(t, out)
	imgs := 0
	walk(doc, func(n *xhtml.Node) {
		if n.Data == "img" {
			imgs++
		}
	})
	// accept and reject: banner and after, less the missing accept-after.
	if imgs != 3 {
		t.Errorf("%d screenshots, want 3", imgs)
	}
	if n := strings.Count(out, "screenshot not taken"); n != 2 {
		t.Errorf("%d 'screenshot not taken', want 2 (the missing file and the path outside)", n)
	}
	// The Deny button at 912,790 120x40 on a 1280x800 viewport.
	if !strings.Contains(out, `<span class="box" style="left: 71.25%; top: 98.75%; width: 9.38%; height: 1.25%"></span>`) {
		i := strings.Index(out, `class="box"`)
		t.Errorf("box: %s", out[max(0, i-10):min(len(out), i+120)])
	}
	// Not a PNG.
	if err := os.WriteFile(filepath.Join(dir, "evidence/page-1/reject-after.png"), []byte("GIF89a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := consentHTMLOf(t, r, ConsentHTMLOptions{EvidenceDir: dir}); !strings.Contains(out, "screenshot isn&#39;t a PNG") {
		t.Errorf("a file that isn't a PNG was embedded")
	}
}

// docsClassifier is the fake catalogue with vendor docs, one of them
// not https.
type docsClassifier struct{ *consenttest.FakeClassifier }

func (c docsClassifier) Trackers() []consent.TrackerInfo {
	out := c.FakeClassifier.Trackers()
	for i := range out {
		switch out[i].ID {
		case "meta-pixel":
			out[i].Docs = "https://developers.facebook.com/docs/meta-pixel/"
		case "ga4":
			out[i].Docs = "javascript:alert(1)"
		}
	}
	return out
}

func TestConsentReportDocsLinks(t *testing.T) {
	consenttest.Use(t, consent.Stages{Classifier: docsClassifier{&consenttest.FakeClassifier{}}})
	r := judgedResult(t, "meta-ignores")
	out := consentHTMLOf(t, r, ConsentHTMLOptions{})
	links := 0
	walk(parseHTML(t, out), func(n *xhtml.Node) {
		href := attr(n, "href")
		if href == "" || strings.HasPrefix(href, "#") {
			return
		}
		links++
		if href != "https://developers.facebook.com/docs/meta-pixel/" || attr(n, "rel") != "noreferrer noopener" {
			t.Errorf("link %q rel %q", href, attr(n, "rel"))
		}
	})
	if links == 0 {
		t.Errorf("no docs link")
	}
	if strings.Contains(out, "javascript:") {
		t.Errorf("a javascript: docs URL was used")
	}
}

// The verdict bar shows what changes the meaning of the result, and the
// footer is PRD 15.
func TestConsentReportSections(t *testing.T) {
	r := judgedResult(t, "meta-ignores")
	out := consentHTMLOf(t, r, ConsentHTMLOptions{ConsentSHA256: testHash})
	doc := parseHTML(t, out)
	text := htmlText(doc)
	var heads []string
	var footer string
	walk(doc, func(n *xhtml.Node) {
		switch n.Data {
		case "h2":
			heads = append(heads, textContent(n))
		case "footer":
			footer = strings.TrimSpace(textContent(n))
		}
	})
	want := []string{"Pages and runs", "Issues", "Banners and choices", "Timelines", "Consent Mode", "Run details"}
	if !slices.Equal(heads, want) {
		t.Errorf("sections %q, want %q", heads, want)
	}
	for _, s := range []string{"Findings", "Evidence, not legal advice", "consent.json sha256 " + testHash} {
		if i := strings.Index(text, s); i < 0 || i > strings.Index(text, "Pages and runs") {
			t.Errorf("verdict bar lacks %q", s)
		}
	}
	if want := strings.ReplaceAll(strings.ReplaceAll(consent.HonestLimits, "`", ""), "\n\n", ""); footer != want {
		t.Errorf("footer\n got %q\nwant %q", footer, want)
	}
	if !strings.HasSuffix(strings.TrimSpace(text), "public Rekor log.") {
		t.Errorf("the report doesn't end with the honest limits")
	}
	r.Sandbox, r.TLSVerified, r.Held = false, false, false
	consentEvaluate(t, r)
	text = htmlText(parseHTML(t, consentHTMLOf(t, r, ConsentHTMLOptions{})))
	for _, want := range []string{"sandbox off", "TLS not verified", "Tracker hits were sent for real (--send-hits)"} {
		if !strings.Contains(text, want) {
			t.Errorf("verdict bar lacks %q", want)
		}
	}
	// Hits after Reject are outlined red; a fixed palette colours trackers.
	if !strings.Contains(out, `class="hit c`) || !regexp.MustCompile(`class="hit c\d+ late"`).MatchString(out) {
		t.Errorf("no late hit marks")
	}
	if !strings.Contains(out, `<line class="choice"`) || !strings.Contains(out, `<polygon class="signal"`) || !strings.Contains(out, `<circle class="cookie c`) {
		t.Errorf("timeline lacks the choice, a signal or a cookie")
	}
}

func TestConsentReportPalette(t *testing.T) {
	h := &consentHTML{colors: map[string]int{}}
	for i := range paletteSize {
		if got, want := h.color(fmt.Sprintf("t%d", i)), fmt.Sprintf("c%d", i); got != want {
			t.Errorf("tracker %d: %s, want %s", i, got, want)
		}
	}
	if got := h.color("t12"); got != "cg" {
		t.Errorf("the thirteenth tracker: %s, want cg", got)
	}
	if got := h.color("t3"); got != "c3" {
		t.Errorf("a tracker changed colour: %s", got)
	}
	tmpl := consentHTMLSource
	for i := range paletteSize {
		if !strings.Contains(tmpl, fmt.Sprintf(".c%d { --c: #", i)) {
			t.Errorf("the template has no colour for c%d", i)
		}
	}
}

// textContent is the text under n, as a browser joins it.
func textContent(n *xhtml.Node) string {
	if n.Type == xhtml.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(textContent(c))
	}
	return b.String()
}

// htmlText is the visible text of a report, one line per text node.
func htmlText(doc *xhtml.Node) string {
	var b strings.Builder
	var f func(*xhtml.Node)
	f = func(n *xhtml.Node) {
		if n.Type == xhtml.ElementNode && (n.Data == "style" || n.Data == "script" || n.Data == "title" && n.Parent != nil && n.Parent.Data == "head") {
			return
		}
		if n.Type == xhtml.TextNode {
			if s := strings.Join(strings.Fields(n.Data), " "); s != "" {
				b.WriteString(s + "\n")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(doc)
	return b.String()
}

// htmlOutline is the element tree of a report with ids and classes, its
// text nodes indented under it: what a golden compares, never pixels.
func htmlOutline(doc *xhtml.Node) string {
	var b strings.Builder
	var f func(*xhtml.Node, int)
	f = func(n *xhtml.Node, depth int) {
		pad := strings.Repeat("  ", max(depth, 0))
		switch n.Type {
		case xhtml.ElementNode:
			line := pad + n.Data
			for _, k := range []string{"id", "class", "href", "rel"} {
				if v := attr(n, k); v != "" {
					line += " " + k + "=" + v
				}
			}
			if n.Data == "img" {
				line += " src=" + attr(n, "src")[:min(22, len(attr(n, "src")))] + "..."
			}
			b.WriteString(line + "\n")
			if n.Data == "style" || n.Data == "script" {
				return
			}
		case xhtml.TextNode:
			if s := strings.Join(strings.Fields(n.Data), " "); s != "" {
				b.WriteString(pad + "| " + s + "\n")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c, depth+1)
		}
	}
	f(doc, -1)
	return b.String()
}

func TestHTMLText(t *testing.T) {
	for _, name := range consenttest.Goldens {
		r := judgedResult(t, name)
		out := consentHTMLOf(t, r, ConsentHTMLOptions{EvidenceDir: evidenceFor(t, r), ConsentSHA256: testHash})
		doc := parseHTML(t, out)
		golden(t, "html/"+name+".txt", htmlText(doc))
		golden(t, "html/"+name+".outline", htmlOutline(doc))
	}
}

// htmlShape is the element tree without text or attribute values: what
// a page-supplied string must never change.
func htmlShape(doc *xhtml.Node) string {
	var b strings.Builder
	walk(doc, func(n *xhtml.Node) {
		b.WriteString(n.Data)
		for _, a := range n.Attr {
			b.WriteString(" " + a.Key)
		}
		b.WriteString("\n")
	})
	return b.String()
}

func FuzzHTMLReport(f *testing.F) {
	for _, s := range []string{`"><script>alert(1)</script>`, "javascript:alert(1)", "</style><img src=x onerror=alert(1)>", "' onmouseover='x",
		"<!--", "]]>", "<svg onload=alert(1)>", "\x00\x1b[2J", "</title></head>", "{{.CSP}}"} {
		f.Add(s)
	}
	base := judgedResult(f, "meta-ignores")
	set := func(r *consent.Result, s string) {
		pg := &r.Pages[0]
		pg.CMP.Name, pg.CMP.DetectedBy = s, s
		pg.Site = s
		for i := range pg.Runs {
			run := &pg.Runs[i]
			for j := range run.Hits {
				run.Hits[j].URL, run.Hits[j].Name = s, s
			}
			for j := range run.Visits {
				run.Visits[j].URL = s
			}
			for j := range run.Cookies {
				run.Cookies[j].Name, run.Cookies[j].Domain = s, s
			}
			for j := range run.Scripts {
				run.Scripts[j].URL = s
			}
			if run.Choice != nil {
				run.Choice.Text, run.Choice.Selector, run.Choice.VerifiedBy = s, s, s
			}
		}
		for i := range r.Issues {
			r.Issues[i].Title, r.Issues[i].Detail, r.Issues[i].Fix, r.Issues[i].Check, r.Issues[i].Key = s, s, s, s, s
		}
		r.Notes = append(r.Notes, s)
		r.Echo, r.Browser, r.UserAgent, r.ID, r.Evidence.Folder = s, s, s, s, s
	}
	f.Fuzz(func(t *testing.T, s string) {
		plain, hostile := consenttest.CloneResult(base), consenttest.CloneResult(base)
		set(plain, "x")
		set(hostile, "x"+s)
		want := htmlShape(parseHTML(t, consentHTMLOf(t, plain, ConsentHTMLOptions{})))
		out := consentHTMLOf(t, hostile, ConsentHTMLOptions{})
		doc := parseHTML(t, out)
		if got := htmlShape(doc); got != want {
			t.Fatalf("%q changed the page's elements or attributes", s)
		}
		walk(doc, func(n *xhtml.Node) {
			for _, a := range n.Attr {
				v := strings.ToLower(strings.TrimSpace(a.Val))
				if (a.Key == "href" || a.Key == "src") && strings.HasPrefix(v, "javascript:") {
					t.Fatalf("%q made a javascript: URL", s)
				}
			}
		})
	})
}
