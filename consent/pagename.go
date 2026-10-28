// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package consent

import (
	"net/url"
	"strings"
)

// PagePath is the path of a page URL without its query: a query often
// changes between runs (campaign tags, cache busters) and may carry
// values a summary mustn't show. Text that isn't a URL with a host is
// kept whole.
func PagePath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	p := u.EscapedPath()
	if p == "" {
		p = "/"
	}
	return p
}

// pageHost is the host (and port) of a page URL, "" when it has none.
func pageHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Host)
}

// MultiHost reports whether the pages of r that share site span more
// than one host, so a path alone can't tell them apart.
func MultiHost(r *Result, site string) bool {
	first := ""
	for i := range r.Pages {
		pg := &r.Pages[i]
		if pg.Site != site {
			continue
		}
		h := pageHost(pg.URL)
		if h == "" {
			continue
		}
		if first == "" {
			first = h
		} else if h != first {
			return true
		}
	}
	return false
}

// PageName is how issues, inconclusive runs and comparisons name pg of
// r: its path, or host and path ("shop.test/about/") when the pages of
// its --sites name (or of the whole check without --sites) span more
// than one host, so two hosts sharing a path stay apart.
func PageName(r *Result, pg *Page) string {
	p := PagePath(pg.URL)
	if h := pageHost(pg.URL); h != "" && r != nil && MultiHost(r, pg.Site) {
		return h + p
	}
	return p
}

// SitePage is the page part of a key: the page name, after the --sites
// name and a colon when there is one ("shop:/about/").
func SitePage(site, page string) string {
	if site == "" {
		return page
	}
	return site + ":" + page
}

// SiteIssueKey is IssueKey for an issue of a --sites site: the page part
// carries the site name, so two sites sharing a path keep apart keys.
func SiteIssueKey(site, check, page string, run RunKind, tracker string) string {
	return IssueKey(check, SitePage(site, page), run, tracker)
}

// PageMatches reports whether want, a page as an ignore: entry or a
// flag names it, names page, an issue's page name. want may be a path
// ("/about/"), which matches that path on any host, a host and path
// ("shop.test/about/") or a whole URL, which matches by host and path
// when page carries a host and by path when it doesn't.
func PageMatches(want, page string) bool {
	want = strings.TrimSpace(want)
	if want == "" || want == page {
		return true
	}
	pagePath := page
	pageHasHost := !strings.HasPrefix(page, "/")
	if pageHasHost {
		if i := strings.Index(page, "/"); i > 0 {
			pagePath = page[i:]
		}
	}
	if u, err := url.Parse(want); err == nil && u.Host != "" {
		wp := PagePath(want)
		if pageHasHost {
			return strings.ToLower(u.Host)+wp == page
		}
		return wp == page
	}
	if strings.HasPrefix(want, "/") {
		return pageHasHost && want == pagePath
	}
	return false
}
