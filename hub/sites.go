// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package hub

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Site is one staging site the hub collects for.
type Site struct {
	ID       string `yaml:"id"`        // client-a: letters, digits, dots, dashes, underscores
	TokenEnv string `yaml:"token_env"` // the environment variable holding the site's write token
	Config   string `yaml:"config"`    // the site's .echo/config.yaml, for schedule
	Schedule string `yaml:"schedule"`  // when schedule runs it, e.g. "Tue 02:00" (informational for the hub)

	token string
}

// Sites is a sites.yaml file:
//
//	read_token_env: ECHO_HUB_READ_TOKEN   # default RHYTHM_ECHO_TOKEN
//	sites:
//	  - id: client-a
//	    token_env: ECHO_TOKEN_CLIENT_A
//	    config: /srv/sites/client-a/.echo/config.yaml
//	    schedule: "Tue 02:00"
type Sites struct {
	ReadTokenEnv string `yaml:"read_token_env"`
	Sites        []Site `yaml:"sites"`

	readToken string
	dir       string // the file's folder: relative config paths start there
}

var siteID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidSiteID reports whether id can name a site: it starts with a letter
// or digit and holds no slash, so it stays one folder in the store.
func ValidSiteID(id string) bool { return siteID.MatchString(id) }

// LoadSites reads a sites file and the tokens its variables name, through
// getenv (os.Getenv in production). Every site needs its own non-empty
// token, none may equal the read token and the read token must be set:
// a hub reachable from other machines never runs open.
func LoadSites(path string, getenv func(string) string) (*Sites, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the operator's own file
	if err != nil {
		return nil, err
	}
	var s Sites
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	s.dir = filepath.Dir(path)
	if s.ReadTokenEnv == "" {
		s.ReadTokenEnv = "RHYTHM_ECHO_TOKEN"
	}
	if s.readToken = getenv(s.ReadTokenEnv); s.readToken == "" {
		return nil, fmt.Errorf("%s: set %s to the hub's read token", path, s.ReadTokenEnv)
	}
	if len(s.Sites) == 0 {
		return nil, fmt.Errorf("%s: no sites; list them under the sites key", path)
	}
	seenID, seenToken := map[string]bool{}, map[string]string{}
	for i := range s.Sites {
		site := &s.Sites[i]
		switch {
		case !siteID.MatchString(site.ID):
			return nil, fmt.Errorf("%s: site %d: id %q: use letters, digits, dots, dashes and underscores", path, i+1, site.ID)
		case seenID[site.ID]:
			return nil, fmt.Errorf("%s: site %q is listed twice", path, site.ID)
		case site.TokenEnv == "":
			return nil, fmt.Errorf("%s: site %q: token_env names no variable", path, site.ID)
		}
		seenID[site.ID] = true
		site.token = getenv(site.TokenEnv)
		switch {
		case site.token == "":
			return nil, fmt.Errorf("%s: site %q: set %s to its write token", path, site.ID, site.TokenEnv)
		case site.token == s.readToken:
			return nil, fmt.Errorf("%s: site %q: its token is the read token; give each site a token of its own", path, site.ID)
		case seenToken[site.token] != "":
			return nil, fmt.Errorf("%s: sites %q and %q share a token; give each site a token of its own", path, seenToken[site.token], site.ID)
		}
		seenToken[site.token] = site.ID
		if site.Config != "" && !filepath.IsAbs(site.Config) {
			site.Config = filepath.Join(s.dir, site.Config)
		}
	}
	return &s, nil
}

// MinToken is the shortest token Echo takes when it listens beyond
// loopback: token checks aren't rate limited, so a short one can be
// guessed from the network. TokenHint says how to make one.
const (
	MinToken  = 16
	TokenHint = "make one with: openssl rand -hex 16"
)

// ShortTokens names the variables (the read token's, then each site's)
// whose token is shorter than MinToken.
func (s *Sites) ShortTokens() []string {
	var out []string
	if len(s.readToken) < MinToken {
		out = append(out, s.ReadTokenEnv)
	}
	for _, site := range s.Sites {
		if len(site.token) < MinToken {
			out = append(out, site.TokenEnv)
		}
	}
	return out
}

// ShortTokenError says that the variables in short hold tokens too
// short for an Echo that listens on addr, beyond loopback.
func ShortTokenError(short []string, addr string) error {
	return fmt.Errorf("%s: shorter than %d characters, which can be guessed while %s is reachable from other machines; %s",
		strings.Join(short, ", "), MinToken, addr, TokenHint)
}

// ReadToken is the read token, from the variable read_token_env names.
func (s *Sites) ReadToken() string { return s.readToken }

// WriteTokens maps each site's write token to its ID, the way
// catcher.Config.WriteTokens takes them.
func (s *Sites) WriteTokens() map[string]string {
	out := make(map[string]string, len(s.Sites))
	for _, site := range s.Sites {
		out[site.token] = site.ID
	}
	return out
}

// Site returns the site with id.
func (s *Sites) Site(id string) (*Site, bool) {
	for i := range s.Sites {
		if s.Sites[i].ID == id {
			return &s.Sites[i], true
		}
	}
	return nil, false
}

// Select returns the sites a --sites value names: "all", or IDs separated
// by commas.
func (s *Sites) Select(list string) ([]*Site, error) {
	var out []*Site
	if list == "" || list == "all" {
		for i := range s.Sites {
			out = append(out, &s.Sites[i])
		}
		return out, nil
	}
	for _, id := range strings.Split(list, ",") {
		id = strings.TrimSpace(id)
		site, ok := s.Site(id)
		if !ok {
			var ids []string
			for _, x := range s.Sites {
				ids = append(ids, x.ID)
			}
			return nil, fmt.Errorf("no site %q; the sites are: %s", id, strings.Join(ids, ", "))
		}
		out = append(out, site)
	}
	return out, nil
}

// SecretEnv names the variables that hold the hub's tokens: the read
// token's and every site's token_env.
func (s *Sites) SecretEnv() []string {
	names := []string{s.ReadTokenEnv}
	for _, site := range s.Sites {
		names = append(names, site.TokenEnv)
	}
	return names
}

// ProjectDir is the folder a site's --after command runs in: the one that
// holds .echo/, or the config file's own folder.
func (site *Site) ProjectDir() string {
	dir := filepath.Dir(site.Config)
	if filepath.Base(dir) == ".echo" {
		return filepath.Dir(dir)
	}
	return dir
}
