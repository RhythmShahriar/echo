// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/store"
)

// consentBlock is what init --consent adds: the consent: block with
// every key at its default, commented out with what it does, so the
// file still checks as written and each key is one uncomment away.
const consentBlock = `
# Cookie consent checks: rhythm echo consent (rhythm echo consent --help).
# Every key is optional; uncomment one to change its default.
consent:
  # urls: [/, /shop/]                 # pages to check, read against site:; absolute URLs work too
  # runs: [none, accept, reject]      # add gpc or withdraw, or write +gpc to add it to these
  # pages: 1                          # more same-site pages after the choice (0 to 10)
  # settle: 3s                        # how long the network must be quiet after each step (at most 30s)
  # banner_wait: 10s                  # how long to wait for a banner (at most 60s)
  # timeout: 90s                      # longest time for one run (at most 10m)
  # retries: 1                        # tries again for a run that couldn't make its choice (0 to 3)
  # cmp: auto                         # or the banner to expect: cookiebot, onetrust, cookieyes, complianz...
  # click: button                     # button, api or button,api
  # accept_selector: "#accept-all"    # only when the CMP rule doesn't find the Accept button
  # reject_selector: "#manage >> #reject-all"  # a >> b clicks a, then b
  # consent_mode: advanced            # advanced, basic or strict
  # geo: de                           # look like a visitor from this country (language and time zone)
  # locale: de-DE                     # default the machine's
  # timezone: Europe/Berlin           # default the machine's
  # viewport: desktop                 # desktop or mobile
  # parallel: 3                       # browser runs at once (1 to 8)
  # exempt: [plausible]               # trackers allowed before consent
  # headers:                          # values must be ${env:RHYTHM_ECHO_*}, never the secret itself
  #   Authorization: ${env:RHYTHM_ECHO_STAGING_AUTH}
  # map_hosts:                        # answer these hosts from another address, for staging
  #   "*.shop.test": 127.0.0.1:18950
  # ignore:                           # known issues; each needs a reason and a date within a year
  #   - check: consent/before-choice
  #     tracker: hotjar
  #     reason: "client signs off until the launch"
  #     until: {{until}}
  # trackers: []                      # your own trackers, the shape of the built-in catalog
`

// consentBlockAt is consentBlock with the ignore example's until: set
// to 30 days after now, so the example is a date still to come when
// someone uncomments it.
func consentBlockAt(now time.Time) string {
	return strings.Replace(consentBlock, "{{until}}", now.AddDate(0, 0, 30).Format(time.DateOnly), 1)
}

// errHasConsent is init --consent's error for a config that already
// has a consent: block.
var errHasConsent = errors.New("the config already has a consent block")

// initConsent adds the consent: block to the config at path, or writes
// a config with only that block when there is none. With force an
// existing consent: block is replaced and the rest of the file kept:
// someone regenerating the block shouldn't lose their actions. It
// returns whether it appended to a config and whether that replaced a
// consent: block.
func initConsent(path string, force bool) (appended, replaced bool, err error) {
	old, err := os.ReadFile(path) //nolint:gosec // the project's own config
	switch {
	case errors.Is(err, fs.ErrNotExist):
		old = nil
	case err != nil:
		return false, false, err
	default:
		var root yaml.Node
		if err := yaml.Unmarshal(old, &root); err != nil {
			return false, false, fmt.Errorf("%s: %w", path, err)
		}
		if hasKey(&root, "consent") {
			if !force {
				return false, false, errHasConsent
			}
			old, replaced = cutTopKey(old, &root, "consent"), true
		}
	}
	// .echo/ will hold consent results, so only its owner may open it.
	if err := os.MkdirAll(config.Dir, 0o700); err != nil {
		return false, false, err
	}
	var b bytes.Buffer
	if len(old) > 0 {
		b.Write(old)
		if !bytes.HasSuffix(old, []byte("\n")) {
			b.WriteByte('\n')
		}
		b.WriteString(consentBlockAt(time.Now()))
	} else {
		b.WriteString("# Echo's settings: https://github.com/RhythmShahriar/echo#configuration\n")
		b.WriteString(consentBlockAt(time.Now()))
	}
	// Written beside the config and loaded before it takes its place:
	// a block that broke the config would be a bug here, never the
	// user's to find on the next command.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.yaml")
	if err != nil {
		return false, false, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(b.Bytes()); err != nil {
		_ = tmp.Close()
		return false, false, err
	}
	if err := tmp.Close(); err != nil {
		return false, false, err
	}
	if _, err := config.Load(tmp.Name()); err != nil {
		return false, false, fmt.Errorf("the consent: block would break %s: %w", path, err)
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil { //nolint:gosec // a project file meant to be committed
		return false, false, err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return false, false, err
	}
	store.Store{Root: config.Dir}.ExcludeFromGit()
	return len(old) > 0, replaced, nil
}

// cutTopKey returns data without the top-level key of root (its line,
// its value up to the next top-level key and the comment lines right
// above it), so init --consent --force can put a fresh block in its
// place. Comments at the left edge just above the next key stay with
// that key.
func cutTopKey(data []byte, root *yaml.Node, key string) []byte {
	m := root.Content[0]
	lines := yamlLines(string(data))
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value != key {
			continue
		}
		start, end := m.Content[i].Line-1, len(lines)
		if i+2 < len(m.Content) {
			end = m.Content[i+2].Line - 1
			for end > start+1 && (strings.TrimSpace(lines[end-1]) == "" || strings.HasPrefix(lines[end-1], "#")) {
				end--
			}
		}
		for start > 0 && strings.HasPrefix(lines[start-1], "#") {
			start--
		}
		kept := strings.TrimRight(strings.Join(slices.Concat(lines[:start], lines[end:]), ""), "\r\n")
		if strings.TrimSpace(kept) == "" {
			return nil
		}
		return []byte(kept + "\n")
	}
	return data
}

// yamlLines splits s after each line break YAML counts (\r\n, \n or a
// lone \r), so its lines match the parser's line numbers.
func yamlLines(s string) []string {
	var lines []string
	for len(s) > 0 {
		i := strings.IndexAny(s, "\r\n")
		if i < 0 {
			break
		}
		n := i + 1
		if s[i] == '\r' && n < len(s) && s[n] == '\n' {
			n++
		}
		lines, s = append(lines, s[:n]), s[n:]
	}
	if len(s) > 0 {
		lines = append(lines, s)
	}
	return lines
}

// hasKey reports whether the YAML document root has key at its top.
func hasKey(root *yaml.Node, key string) bool {
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return false
	}
	m := root.Content[0]
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return true
		}
	}
	return false
}

// cmdInitConsent is init --consent.
func cmdInitConsent(force bool, stdout, stderr io.Writer) int {
	appended, replaced, err := initConsent(config.Path(), force)
	// Messages name the file with forward slashes on every OS, as the
	// consent command's messages do.
	path := filepath.ToSlash(config.Path())
	switch {
	case errors.Is(err, errHasConsent):
		fmt.Fprintf(stderr, "rhythm echo: %s already has a consent: block; pass --force to replace only that block\n", path)
		return exitUsage
	case err != nil:
		fmt.Fprintln(stderr, "rhythm echo:", err)
		return exitError
	case appended && replaced:
		fmt.Fprintf(stdout, "Replaced the consent: block in %s; the rest of it is unchanged.\n", path)
	case appended:
		fmt.Fprintf(stdout, "Added a consent: block to %s; uncomment a key to change its default.\n", path)
	default:
		fmt.Fprintf(stdout, "Wrote %s with a consent: block; uncomment a key to change its default.\n", path)
	}
	fmt.Fprintln(stdout, "Then: rhythm echo consent https://staging.shop.test (or list pages under consent: urls).")
	return exitOK
}
