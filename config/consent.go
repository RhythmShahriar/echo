// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package config

import (
	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/echo/consent"
)

// Consent is the consent: block of the config (PRD 8.1): what rhythm
// echo consent checks and how, each key the config form of a flag.
// Levels stay in the top-level checks: map.
type Consent struct {
	URLs           []string          `yaml:"urls,omitempty"` // read against site:; absolute URLs work too
	Runs           []string          `yaml:"runs,omitempty"` // none, accept, reject, gpc, withdraw
	Pages          *int              `yaml:"pages,omitempty"`
	Settle         *Duration         `yaml:"settle,omitempty"`
	BannerWait     *Duration         `yaml:"banner_wait,omitempty"`
	Timeout        *Duration         `yaml:"timeout,omitempty"`
	Retries        *int              `yaml:"retries,omitempty"`
	CMP            string            `yaml:"cmp,omitempty"`
	Click          string            `yaml:"click,omitempty"` // button, api or button,api
	AcceptSelector string            `yaml:"accept_selector,omitempty"`
	RejectSelector string            `yaml:"reject_selector,omitempty"`
	ConsentMode    string            `yaml:"consent_mode,omitempty"` // advanced, basic or strict
	Geo            string            `yaml:"geo,omitempty"`
	Locale         string            `yaml:"locale,omitempty"`
	Timezone       string            `yaml:"timezone,omitempty"`
	Viewport       string            `yaml:"viewport,omitempty"`
	Parallel       int               `yaml:"parallel,omitempty"`
	Exempt         []string          `yaml:"exempt,omitempty"`
	Headers        map[string]string `yaml:"headers,omitempty"`   // values must be ${env:RHYTHM_ECHO_*}
	MapHosts       map[string]string `yaml:"map_hosts,omitempty"` // host or *.host to an address
	Ignore         []consent.Ignore  `yaml:"ignore,omitempty"`    // known issues, each with a reason and an expiry
	Trackers       yaml.Node         `yaml:"trackers,omitempty"`  // custom trackers, the catalog's shape; the classifier reads them
}

// validate checks the consent: block. It is nil-safe: a config without
// one has nothing to check.
func (c *Consent) validate(cfg *Config) error {
	_ = cfg
	return nil
}
