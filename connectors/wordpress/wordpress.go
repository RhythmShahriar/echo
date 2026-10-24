// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package wordpress installs Echo's WordPress connector: one mu-plugin,
// embedded in the binary, that reports every wp_mail() and wp_remote_*()
// call to Echo and obeys its seal. It can be written into a WordPress tree
// on disk (Install), or handed to WP-CLI (`wp eval-file`) or `docker cp`
// for sites Echo can't write to directly (Script, Files).
//
// The plugin lives at wp-content/mu-plugins/rhythm-echo.php; its settings,
// when any are given, at wp-content/mu-plugins/rhythm-echo/settings.php.
// mu-plugins only loads top-level files, so the settings file is never run
// as a plugin, and it is PHP, so the web server never serves the token.
//
// The same connector also ships as a regular plugin, Echo Staging Seal
// (plugin/echo-staging-seal), for WordPress.org: it adds an admin bar
// banner, Tools > Echo and `wp echo` commands. PluginZip builds its zip and
// InstallPlugin writes it into a site. It reads the same settings file.
package wordpress

import (
	"archive/zip"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

//go:embed echo.php
var plugin []byte

// pluginTree is Echo Staging Seal without its connector, which PluginFiles
// adds from echo.php so the two never drift.
//
//go:embed plugin/echo-staging-seal
var pluginTree embed.FS

// Paths inside wp-content.
const (
	PluginPath   = "mu-plugins/rhythm-echo.php"
	SettingsPath = "mu-plugins/rhythm-echo/settings.php"
)

// Echo Staging Seal, the regular plugin: its slug (the folder in plugins/
// and in the zip) and where it goes inside wp-content.
const (
	PluginSlug = "echo-staging-seal"
	PluginDir  = "plugins/" + PluginSlug
	// connectorFile is where echo.php goes inside the plugin.
	connectorFile = "includes/connector.php"
)

// The plugin's REST routes for `rhythm echo doctor`, under /wp-json/. They
// exist only while the connector is active, so a production site answers
// 404. Each call needs X-Echo-Token equal to the configured token or a
// logged-in administrator. With no token configured, a request from
// loopback with no X-Forwarded-For, Forwarded or X-Real-IP header is let in
// too. Anything else gets 403.
const (
	PingRoute   = "rhythm-echo/v1/ping"   // GET: active, layers, warnings, can the site reach Echo
	CanaryRoute = "rhythm-echo/v1/canary" // POST: sends one wp_mail and one wp_remote_post to the addresses below
)

// What the canary route sends to: addresses that can't exist, so a
// canary that isn't caught goes nowhere.
const (
	CanaryEmail = "canary@echo.invalid"
	CanaryURL   = "https://canary.echo.invalid/"
)

// marker identifies a file this package wrote, so Uninstall never removes
// someone else's rhythm-echo.php.
const marker = "rhythm-echo-connector"

// Settings configure the connector. Empty fields are left out, and the
// plugin then falls back to RHYTHM_ECHO_* constants and environment
// variables (which also override the file), then to its defaults: Echo at
// http://127.0.0.1:8025, sealed.
type Settings struct {
	URL   string // Echo's capture API as the site reaches it, e.g. http://host.docker.internal:8025
	Token string // sent as X-Echo-Token; also guards the plugin's REST routes and checks X-Echo-Run
	// WriteToken is a token that can only send captures (a hub's per-site
	// token). It is sent instead of Token; without Token it also does
	// Token's other jobs.
	WriteToken string
	// SiteID names the site in every capture (source.site) when one Echo
	// serves many.
	SiteID string
	Mode   string // seal or observe: only decides what happens when Echo can't be reached (seal fails closed)
	Force  bool   // run even when WordPress says this is production
	// Proxy is Echo's HTTP(S) proxy as the site reaches it, e.g.
	// http://127.0.0.1:8026. When set, the plugin points raw cURL at it
	// (https_proxy, http_proxy, no_proxy for the site, Echo and loopback),
	// catching SDKs that skip the WordPress HTTP API.
	Proxy string
	// CA is the path, on the site's machine, to Echo's CA certificate (PEM).
	// It is added to the WordPress HTTP API's CA bundle, not replacing it.
	CA string
	// Bin is the rhythm-echo binary on the site's machine, which Echo
	// Staging Seal's buttons and `wp echo` commands run. Project is the
	// folder with .echo/config.yaml it runs in. The mu-plugin ignores both.
	Bin     string
	Project string
}

// Plugin returns the mu-plugin's PHP source.
func Plugin() []byte { return append([]byte(nil), plugin...) }

// Empty reports whether s sets nothing, so no settings file is needed.
func (s Settings) Empty() bool { return s == Settings{} }

// SettingsPHP returns the settings file: a PHP file returning an array.
func SettingsPHP(s Settings) ([]byte, error) {
	if s.Mode != "" && s.Mode != "seal" && s.Mode != "observe" {
		return nil, fmt.Errorf("mode must be seal or observe, not %q", s.Mode)
	}
	var b strings.Builder
	b.WriteString("<?php\n// Rhythm Echo connector settings, written by `rhythm echo install wordpress`.\n")
	b.WriteString("// RHYTHM_ECHO_* constants and environment variables override these.\n")
	b.WriteString("// " + marker + "\nreturn array(\n")
	for _, kv := range [][2]string{{"url", s.URL}, {"token", s.Token}, {"write_token", s.WriteToken}, {"site_id", s.SiteID}, {"mode", s.Mode}, {"proxy", s.Proxy}, {"ca", s.CA}, {"bin", s.Bin}, {"project", s.Project}} {
		if kv[1] != "" {
			fmt.Fprintf(&b, "\t%s => %s,\n", phpString(kv[0]), phpString(kv[1]))
		}
	}
	if s.Force {
		b.WriteString("\t'force' => true,\n")
	}
	b.WriteString(");\n")
	return []byte(b.String()), nil
}

// Files returns what to write, keyed by path relative to wp-content: the
// plugin, and the settings file unless s is empty. For `docker cp` and
// similar.
func Files(s Settings) (map[string][]byte, error) {
	out := map[string][]byte{PluginPath: Plugin()}
	if !s.Empty() {
		b, err := SettingsPHP(s)
		if err != nil {
			return nil, err
		}
		out[SettingsPath] = b
	}
	return out, nil
}

// Install writes the connector into the WordPress at dir: the site root
// (with wp-config.php or wp-content/) or its wp-content directory. Any
// earlier settings file is replaced, or removed when s is empty, so the
// result matches s.
func Install(dir string, s Settings) error {
	content, err := ContentDir(dir)
	if err != nil {
		return err
	}
	files, err := Files(s)
	if err != nil {
		return err
	}
	if ok, err := ours(filepath.Join(content, PluginPath)); err == nil && !ok {
		return fmt.Errorf("%s exists and wasn't written by Echo; remove it first", filepath.Join(content, PluginPath))
	}
	if s.Empty() {
		if err := os.RemoveAll(filepath.Join(content, filepath.Dir(SettingsPath))); err != nil {
			return err
		}
	}
	for _, rel := range sortedKeys(files) {
		path := filepath.Join(content, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // the web server must read mu-plugins
			return err
		}
		if err := writeFile(path, files[rel]); err != nil {
			return err
		}
	}
	return nil
}

// Uninstall removes the connector from the WordPress at dir. It reports
// fs.ErrNotExist when there was nothing to remove.
func Uninstall(dir string) error {
	content, err := ContentDir(dir)
	if err != nil {
		return err
	}
	path := filepath.Join(content, PluginPath)
	ok, err := ours(path)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s wasn't written by Echo; leaving it alone", path)
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	if pluginOurs(content) {
		return nil // Echo Staging Seal reads the same settings
	}
	return os.RemoveAll(filepath.Join(content, filepath.Dir(SettingsPath)))
}

// Installed reports whether the connector is in the WordPress at dir.
func Installed(dir string) bool {
	content, err := ContentDir(dir)
	if err != nil {
		return false
	}
	ok, err := ours(filepath.Join(content, PluginPath))
	return err == nil && ok
}

// ours reports whether the plugin file at path was written by Echo.
func ours(path string) (bool, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the path is the site's own plugin file, chosen by the caller
	if err != nil {
		return false, err
	}
	return strings.Contains(string(b), marker), nil
}

// ContentDir finds wp-content for dir: dir itself when it is wp-content
// (has plugins/, themes/ or mu-plugins/), or dir/wp-content when dir is a
// site root.
func ContentDir(dir string) (string, error) {
	if isDir(filepath.Join(dir, "wp-content")) {
		return filepath.Join(dir, "wp-content"), nil
	}
	for _, sub := range []string{"plugins", "themes", "mu-plugins"} {
		if isDir(filepath.Join(dir, sub)) {
			return dir, nil
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "wp-config.php")); err == nil {
		return filepath.Join(dir, "wp-content"), nil
	}
	return "", fmt.Errorf("%s doesn't look like WordPress: no wp-content/ or wp-config.php", dir)
}

// Script returns PHP for `wp eval-file` that installs the connector into
// the running site's mu-plugins directory (WPMU_PLUGIN_DIR, so custom
// layouts work), for sites reached only through WP-CLI: over SSH, or
// `docker exec` into a container. Pipe it in with `wp eval-file -`.
func Script(s Settings) (string, error) {
	files, err := Files(s)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("<?php\n// Installs the Rhythm Echo connector. Run with: wp eval-file - < this file\n")
	b.WriteString("$dir = defined( 'WPMU_PLUGIN_DIR' ) ? WPMU_PLUGIN_DIR : WP_CONTENT_DIR . '/mu-plugins';\n")
	b.WriteString("$files = array(\n")
	for _, rel := range sortedKeys(files) {
		fmt.Fprintf(&b, "\t%s => %s,\n", phpString(strings.TrimPrefix(rel, "mu-plugins/")), phpBase64(files[rel]))
	}
	b.WriteString(");\n")
	b.WriteString(`$old = $dir . '/rhythm-echo.php';
if ( file_exists( $old ) && false === strpos( (string) file_get_contents( $old ), '` + marker + `' ) ) {
	fwrite( STDERR, "Error: $old exists and wasn't written by Echo; remove it first\n" );
	exit( 1 );
}
`)
	if s.Empty() {
		b.WriteString("@unlink( $dir . '/rhythm-echo/settings.php' );\n@rmdir( $dir . '/rhythm-echo' );\n")
	}
	b.WriteString(`foreach ( $files as $rel => $content ) {
	$path = $dir . '/' . $rel;
	if ( ! is_dir( dirname( $path ) ) && ! mkdir( dirname( $path ), 0755, true ) ) {
		fwrite( STDERR, "Error: can't create " . dirname( $path ) . "\n" );
		exit( 1 );
	}
	if ( false === file_put_contents( $path, base64_decode( $content ) ) ) {
		fwrite( STDERR, "Error: can't write $path\n" );
		exit( 1 );
	}
}
echo "Installed $dir/rhythm-echo.php\n";
`)
	return b.String(), nil
}

// UninstallScript returns PHP for `wp eval-file -` that removes the
// connector.
func UninstallScript() string {
	return `<?php
// Removes the Rhythm Echo connector. Run with: wp eval-file - < this file
$dir  = defined( 'WPMU_PLUGIN_DIR' ) ? WPMU_PLUGIN_DIR : WP_CONTENT_DIR . '/mu-plugins';
$path = $dir . '/rhythm-echo.php';
if ( ! file_exists( $path ) ) {
	echo "Not installed\n";
	return;
}
if ( false === strpos( (string) file_get_contents( $path ), '` + marker + `' ) ) {
	fwrite( STDERR, "Error: $path wasn't written by Echo; leaving it alone\n" );
	exit( 1 );
}
unlink( $path );
@unlink( $dir . '/rhythm-echo/settings.php' );
@rmdir( $dir . '/rhythm-echo' );
echo "Removed $path\n";
`
}

// writeFile writes atomically, so a request never loads half a plugin.
func writeFile(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".rhythm-echo-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// The web server, usually another user, must read the plugin.
	if err := os.Chmod(tmp.Name(), 0o644); err != nil { //nolint:gosec // see above
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// phpString quotes s as a single-quoted PHP string.
func phpString(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

func phpBase64(b []byte) string {
	return phpString(base64.StdEncoding.EncodeToString(b))
}

func sortedKeys(m map[string][]byte) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// PluginFiles returns Echo Staging Seal's files keyed by path relative to
// its folder: the plugin tree with echo.php as includes/connector.php.
func PluginFiles() (map[string][]byte, error) {
	out := map[string][]byte{connectorFile: Plugin()}
	root := "plugin/" + PluginSlug
	err := fs.WalkDir(pluginTree, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := pluginTree.ReadFile(p)
		if err != nil {
			return err
		}
		out[strings.TrimPrefix(p, root+"/")] = b
		return nil
	})
	return out, err
}

// PluginZip writes Echo Staging Seal as WordPress installs it from a zip
// (Plugins > Add New > Upload, or WordPress.org): every file under
// echo-staging-seal/. The entries are sorted with a fixed time, so the same
// source always gives the same zip.
func PluginZip(w io.Writer) error {
	files, err := PluginFiles()
	if err != nil {
		return err
	}
	z := zip.NewWriter(w)
	when := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, rel := range sortedKeys(files) {
		h := &zip.FileHeader{Name: path.Join(PluginSlug, rel), Method: zip.Deflate, Modified: when}
		h.SetMode(0o644)
		f, err := z.CreateHeader(h)
		if err != nil {
			return err
		}
		if _, err := f.Write(files[rel]); err != nil {
			return err
		}
	}
	return z.Close()
}

// InstallPlugin writes Echo Staging Seal into the WordPress at dir (the
// site root or wp-content) and the settings file, as Install does, unless
// s is empty. The plugin still has to be activated (wp plugin activate
// echo-staging-seal). An existing plugins/echo-staging-seal/ that Echo
// didn't write is left alone.
func InstallPlugin(dir string, s Settings) error {
	content, err := ContentDir(dir)
	if err != nil {
		return err
	}
	settings, err := Files(s)
	if err != nil {
		return err
	}
	files, err := PluginFiles()
	if err != nil {
		return err
	}
	base := filepath.Join(content, PluginDir)
	if entries, err := os.ReadDir(base); err == nil && len(entries) > 0 && !pluginOurs(content) {
		return fmt.Errorf("%s exists and wasn't written by Echo; remove it first", base)
	}
	for _, rel := range sortedKeys(files) {
		p := filepath.Join(base, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil { //nolint:gosec // the web server must read plugins
			return err
		}
		if err := writeFile(p, files[rel]); err != nil {
			return err
		}
	}
	if b, ok := settings[SettingsPath]; ok {
		p := filepath.Join(content, SettingsPath)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil { //nolint:gosec // see above
			return err
		}
		return writeFile(p, b)
	}
	return nil
}

// UninstallPlugin removes Echo Staging Seal from the WordPress at dir, and
// the settings file unless the mu-plugin still uses it. Deactivate it first
// (wp plugin deactivate) so WordPress doesn't report it missing. It reports
// fs.ErrNotExist when there was nothing to remove.
func UninstallPlugin(dir string) error {
	content, err := ContentDir(dir)
	if err != nil {
		return err
	}
	base := filepath.Join(content, PluginDir)
	if !isDir(base) {
		return fmt.Errorf("%s: %w", base, fs.ErrNotExist)
	}
	if !pluginOurs(content) {
		return fmt.Errorf("%s wasn't written by Echo; leaving it alone", base)
	}
	if err := os.RemoveAll(base); err != nil {
		return err
	}
	if Installed(content) {
		return nil
	}
	return os.RemoveAll(filepath.Join(content, filepath.Dir(SettingsPath)))
}

// PluginInstalled reports whether Echo Staging Seal is in the WordPress at
// dir.
func PluginInstalled(dir string) bool {
	content, err := ContentDir(dir)
	return err == nil && pluginOurs(content)
}

// pluginOurs reports whether wp-content's Echo Staging Seal was written by
// Echo: its connector carries the marker.
func pluginOurs(content string) bool {
	ok, err := ours(filepath.Join(content, PluginDir, filepath.FromSlash(connectorFile)))
	return err == nil && ok
}

// IsNotInstalled reports whether err from Uninstall means there was
// nothing to remove.
func IsNotInstalled(err error) bool { return errors.Is(err, fs.ErrNotExist) }
