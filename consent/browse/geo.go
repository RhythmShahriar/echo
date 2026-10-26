// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browse

import (
	"slices"
	"strings"

	"github.com/RhythmShahriar/echo/consent"
)

// Geo is how a visitor from one country looks to a page: the browser's
// language and time zone. --geo never changes the IP address.
type Geo struct {
	Locale   string // BCP 47, e.g. de-DE
	Timezone string // IANA, e.g. Europe/Berlin
}

// geos are the countries --geo knows: the 27 EU member states, the
// other EEA states and Switzerland, the UK and the markets with their
// own consent rules. Each gets its most spoken language and its capital's
// (or most populous region's) time zone.
var geos = map[string]Geo{
	"AT": {"de-AT", "Europe/Vienna"},
	"BE": {"nl-BE", "Europe/Brussels"},
	"BG": {"bg-BG", "Europe/Sofia"},
	"HR": {"hr-HR", "Europe/Zagreb"},
	"CY": {"el-CY", "Asia/Nicosia"},
	"CZ": {"cs-CZ", "Europe/Prague"},
	"DK": {"da-DK", "Europe/Copenhagen"},
	"EE": {"et-EE", "Europe/Tallinn"},
	"FI": {"fi-FI", "Europe/Helsinki"},
	"FR": {"fr-FR", "Europe/Paris"},
	"DE": {"de-DE", "Europe/Berlin"},
	"GR": {"el-GR", "Europe/Athens"},
	"HU": {"hu-HU", "Europe/Budapest"},
	"IE": {"en-IE", "Europe/Dublin"},
	"IT": {"it-IT", "Europe/Rome"},
	"LV": {"lv-LV", "Europe/Riga"},
	"LT": {"lt-LT", "Europe/Vilnius"},
	"LU": {"fr-LU", "Europe/Luxembourg"},
	"MT": {"mt-MT", "Europe/Malta"},
	"NL": {"nl-NL", "Europe/Amsterdam"},
	"PL": {"pl-PL", "Europe/Warsaw"},
	"PT": {"pt-PT", "Europe/Lisbon"},
	"RO": {"ro-RO", "Europe/Bucharest"},
	"SK": {"sk-SK", "Europe/Bratislava"},
	"SI": {"sl-SI", "Europe/Ljubljana"},
	"ES": {"es-ES", "Europe/Madrid"},
	"SE": {"sv-SE", "Europe/Stockholm"},
	"GB": {"en-GB", "Europe/London"},
	"NO": {"nb-NO", "Europe/Oslo"},
	"IS": {"is-IS", "Atlantic/Reykjavik"},
	"LI": {"de-LI", "Europe/Vaduz"},
	"CH": {"de-CH", "Europe/Zurich"},
	"US": {"en-US", "America/New_York"},
	"CA": {"en-CA", "America/Toronto"},
	"BR": {"pt-BR", "America/Sao_Paulo"},
	"AU": {"en-AU", "Australia/Sydney"},
	"JP": {"ja-JP", "Asia/Tokyo"},
	"IN": {"en-IN", "Asia/Kolkata"},
}

// geoAliases are the codes people type for a country whose ISO code is
// another: UK for GB, EL (the EU's own code) for GR.
var geoAliases = map[string]string{"UK": "GB", "EL": "GR"}

// GeoFor returns the locale and time zone for country code cc (ISO
// 3166-1 alpha-2, any case); ok is false for a country --geo doesn't
// know.
func GeoFor(cc string) (Geo, bool) {
	cc = strings.ToUpper(strings.TrimSpace(cc))
	if a, ok := geoAliases[cc]; ok {
		cc = a
	}
	g, ok := geos[cc]
	return g, ok
}

// GeoCountries lists the codes --geo knows, sorted, for the CLI's usage
// error.
func GeoCountries() []string {
	out := make([]string, 0, len(geos))
	for cc := range geos {
		out = append(out, cc)
	}
	slices.Sort(out)
	return out
}

// KnowsGeo reports whether --geo knows country code cc (any case, UK
// and EL included), so the CLI can refuse one it would only ignore.
func (Runner) KnowsGeo(cc string) bool {
	_, ok := GeoFor(cc)
	return ok
}

// planLocale is the locale a run uses: --locale, else --geo's, else the
// machine's, else en-US. Anything not shaped like a language tag is
// en-US.
func planLocale(p *consent.Plan) string {
	l := strings.TrimSpace(p.Locale)
	if l == "" {
		if g, ok := GeoFor(p.Geo); ok && p.Geo != "" {
			l = g.Locale
		}
	}
	if l == "" {
		l = machineLocale(osGetenv)
	}
	if !localeTag.MatchString(l) {
		return "en-US"
	}
	return l
}

// planTimezone is the time zone a run uses: --timezone, else --geo's;
// "" keeps the machine's.
func planTimezone(p *consent.Plan) string {
	if tz := strings.TrimSpace(p.Timezone); tz != "" {
		return tz
	}
	if g, ok := GeoFor(p.Geo); ok && p.Geo != "" {
		return g.Timezone
	}
	return ""
}
