// Package config loads Echo's project settings from .echo/config.yaml:
// how the catcher behaves (seal or observe, what may be reached), what
// counts as staging and production, and the actions `snapshot` and
// `compare` run. Every key is optional.
package config

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
)

// Dir is the project folder Echo keeps its settings, snapshots and runs in.
const Dir = ".echo"

type Config struct {
	Site            string   `yaml:"site"`             // base URL relative actions use, e.g. https://staging.shop.test
	Mode            string   `yaml:"mode"`             // seal (default) or observe
	Listen          string   `yaml:"listen"`           // API address, default 127.0.0.1:8025
	SMTP            string   `yaml:"smtp"`             // SMTP sink address, default 127.0.0.1:1025; "off" disables
	Proxy           string   `yaml:"proxy"`            // HTTP(S) proxy address, default off; e.g. 127.0.0.1:8026
	MITM            bool     `yaml:"mitm"`             // decrypt HTTPS through the proxy (needs Echo's CA trusted)
	Allow           []string `yaml:"allow"`            // hosts reachable even when sealed: ["api.stripe.com", "*.sandbox.paypal.com"]
	AllowRecipients []string `yaml:"allow_recipients"` // addresses or numbers that may really be messaged in observe mode
	Staging         []string `yaml:"staging"`          // URL prefixes that are staging: replay only sends to these (and local addresses)
	Production      []string `yaml:"production"`       // URL prefixes that are live sites: actions refuse them
	Flush           []string `yaml:"flush"`            // commands run after each action so async senders send now
	Quiet           Duration `yaml:"quiet"`            // how long nothing must arrive before an action counts as done; default 2s
	Timeout         Duration `yaml:"timeout"`          // longest wait per action; default 30s
	Actions         []Action `yaml:"actions"`
}

// Action is something that makes the site send messages. Exactly one of
// HTTP, Command or Replay is set.
type Action struct {
	Name    string            `yaml:"name"`
	HTTP    *HTTPAction       `yaml:"http"`
	Command string            `yaml:"command"` // run with sh -c; RHYTHM_ECHO_RUN is set
	Replay  *ReplayAction     `yaml:"replay"`
	Env     map[string]string `yaml:"env"`
	Expect  *Expect           `yaml:"expect"` // optional: fail the action unless these were sent
}

type HTTPAction struct {
	Method  string            `yaml:"method"`
	URL     string            `yaml:"url"` // absolute, or relative to site
	Headers map[string]string `yaml:"headers"`
	Form    map[string]string `yaml:"form"` // sent as application/x-www-form-urlencoded
	JSON    any               `yaml:"json"` // sent as application/json
	Body    string            `yaml:"body"`
}

type ReplayAction struct {
	Event  string            `yaml:"event"` // e.g. payment_intent.succeeded, or a path to a JSON file
	To     string            `yaml:"to"`    // absolute, or relative to site
	Set    map[string]string `yaml:"set"`   // field overrides: data.object.amount: 1999
	Secret string            `yaml:"secret_env"`
}

// Expect lists messages an action must send.
type Expect struct {
	Email   int `yaml:"email"`
	SMS     int `yaml:"sms"`
	Webhook int `yaml:"webhook"`
	Chat    int `yaml:"chat"`
}

// Duration reads "2s" style values.
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := time.ParseDuration(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: %q isn't a duration like 2s", n.Line, n.Value)
	}
	d.Duration = v
	return nil
}

// Default returns the settings used when there's no config file.
func Default() Config {
	return Config{Mode: "seal", Listen: "127.0.0.1:8025", SMTP: "127.0.0.1:1025",
		Quiet: Duration{2 * time.Second}, Timeout: Duration{30 * time.Second}}
}

// Load reads path over the defaults. A missing file is not an error;
// unknown keys are, so typos fail loudly.
func Load(path string) (Config, error) {
	c := Default()
	data, err := os.ReadFile(path) //nolint:gosec // the user's own project config
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	return c, c.Validate()
}

// Validate checks values a user could get wrong.
func (c Config) Validate() error {
	if c.Mode != "seal" && c.Mode != "observe" {
		return fmt.Errorf("mode: %q isn't seal or observe", c.Mode)
	}
	seen := map[string]bool{}
	for i, a := range c.Actions {
		n := 0
		if a.HTTP != nil {
			n++
		}
		if a.Command != "" {
			n++
		}
		if a.Replay != nil {
			n++
		}
		switch {
		case strings.TrimSpace(a.Name) == "":
			return fmt.Errorf("actions[%d]: needs a name", i)
		case n != 1:
			return fmt.Errorf("action %q: set exactly one of http, command or replay", a.Name)
		case seen[a.Name]:
			return fmt.Errorf("action %q is listed twice", a.Name)
		case a.HTTP != nil && a.HTTP.URL == "":
			return fmt.Errorf("action %q: http needs a url", a.Name)
		case a.Replay != nil && (a.Replay.Event == "" || a.Replay.To == ""):
			return fmt.Errorf("action %q: replay needs event and to", a.Name)
		}
		seen[a.Name] = true
	}
	return nil
}

// IsProduction reports whether url is a live site.
func (c Config) IsProduction(url string) bool {
	return slices.ContainsFunc(c.Production, func(p string) bool { return p != "" && strings.HasPrefix(url, p) })
}

// Path is the default config file location.
func Path() string { return filepath.Join(Dir, "config.yaml") }

// Template is what `rhythm echo init` writes. It must stay loadable.
const Template = `# Echo settings. Everything here is optional; delete what you don't change.
# Reference: https://github.com/RhythmShahriar/rhythm/wiki/Echo-Configuration
# Commit this file to share it; snapshots and runs are kept out of git.

site: ""                # base URL for relative action URLs, e.g. https://staging.shop.test
mode: seal              # seal: nothing leaves, Echo answers instead. observe: record and send for real
listen: 127.0.0.1:8025  # API and web view (connectors post here)
smtp: 127.0.0.1:1025    # SMTP sink; "off" to disable
proxy: ""               # HTTP(S) proxy for SDKs that skip the connector, e.g. 127.0.0.1:8026
mitm: false             # decrypt HTTPS through the proxy (trust the CA from: rhythm echo ca)

allow: []               # hosts reachable even when sealed, e.g. ["api.stripe.com"] (test-mode keys only)
allow_recipients: []    # in observe mode, recipients you may really message, e.g. ["*@yourcompany.com"]
staging: []             # URL prefixes that are staging; replay only sends to these and local addresses
production: []          # URL prefixes of live sites; actions refuse them

flush: []               # run after each action so queued mail and webhooks go out now, e.g.
                        #   ["wp cron event run --due-now", "wp action-scheduler run"]
                        #   ["php artisan queue:work --stop-when-empty"]
quiet: 2s               # an action is done when nothing new arrives for this long
timeout: 30s            # longest wait per action

# What snapshot and compare run. Each action makes the site send something.
# Form submits and multi-step journeys are better run by Soundcheck with --echo.
actions: []
#  - name: Contact form
#    http: {method: POST, url: /wp-admin/admin-ajax.php, form: {action: contact, email: "${fake:email}", message: Hello}}
#    expect: {email: 2}
#  - name: Completed order
#    command: wp eval 'do_action("woocommerce_order_status_completed", 123);'
#  - name: Stripe payment succeeded
#    replay: {event: payment_intent.succeeded, to: /?wc-api=wc_stripe, secret_env: ECHO_STRIPE_WEBHOOK_SECRET}
`
