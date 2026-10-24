# Rhythm Echo for WordPress

The WordPress connector reports every `wp_mail()` and `wp_remote_*()` call to Rhythm Echo and obeys its seal. It comes in two forms built from the same file, [`echo.php`](echo.php):

| Form | Where it goes | Installed by | Adds |
|---|---|---|---|
| mu-plugin | `wp-content/mu-plugins/rhythm-echo.php` | `rhythm echo install wordpress` | the seal only, settings in a file |
| Echo Staging Seal | `wp-content/plugins/echo-staging-seal/` | WordPress.org, a zip upload, or `wordpress.InstallPlugin` | the seal, an admin bar banner, Tools > Echo and `wp echo` |

How the connector hooks in (wp_mail, pre_http_request, runs, fail closed, the production guard) is described at the top of `echo.php`.

## Settings

First found wins:

1. `define()` constants in wp-config.php: `RHYTHM_ECHO_URL`, `RHYTHM_ECHO_TOKEN`, `RHYTHM_ECHO_MODE`, `RHYTHM_ECHO_FORCE`, `RHYTHM_ECHO_PROXY`, `RHYTHM_ECHO_CA`.
2. The same names in the environment.
3. `wp-content/mu-plugins/rhythm-echo/settings.php`, written by the installer. Both forms read it, so a plugin update never loses it.
4. Echo Staging Seal only: what Tools > Echo saved (URL, token, the mode for when Echo is down).

Echo Staging Seal's buttons and `wp echo` commands run the `rhythm-echo` binary named by `RHYTHM_ECHO_BIN`, in the folder `RHYTHM_ECHO_PROJECT` (the one with `.echo/config.yaml`). Those two come from the first three sources only, never from the admin screens, so an administrator account alone cannot pick a program for the server to run.

## Echo Staging Seal

- **Banner:** "This is a sealed copy" in the admin bar while Echo seals the site, "Echo is observing: sends go out" when Echo observes. The mode is the one Echo reports, asked at most once a minute.
- **Tools > Echo** (administrators only): the sends Echo stopped, counted by plugin and channel (faked: Echo answered; blocked: Echo refused), the update buttons and the settings form. Every form needs `manage_options` and its nonce.
- **Snapshot before updating** runs `rhythm-echo snapshot --name before-updates`. **Compare since last update** runs `rhythm-echo compare`. Their output shows on the page. Without `RHYTHM_ECHO_BIN`, or where the host disables `proc_open`, the page shows the commands to run yourself.
- **WP-CLI:** `wp echo status`, `wp echo snapshot <name>` and `wp echo compare [--snapshot=<name>]` stream rhythm-echo's output and exit with its code:

  ```sh
  wp echo snapshot before-updates && wp plugin update --all && wp echo compare
  ```

- If the mu-plugin is installed too, it runs the seal with its own settings and the plugin only adds its screens and commands.
- Deleting the plugin removes its option and transients. The installer's settings file stays until `rhythm echo install wordpress --uninstall`.

Build the zip for WordPress.org or Plugins > Add New > Upload:

```sh
GOWORK=off go run ./connectors/wordpress/cmd/plugin-zip -o echo-staging-seal.zip
```

The zip carries the plugin folder with `echo.php` as `includes/connector.php`. Nothing else goes in.

## Development

```sh
GOWORK=off go test ./connectors/wordpress/...                      # unit tests, php -l and the PHP harness tests (host php)
ECHO_PHPSTAN=/path/to/vendor/bin/phpstan GOWORK=off go test -run PHPStan ./connectors/wordpress/
GOWORK=off go test -tags docker -run Docker -v ./connectors/wordpress/   # a real WordPress in Docker
```

PHPStan checks `echo.php` at level 6 (`phpstan.neon.dist`) and the plugin's own code at level max (`phpstan-plugin.neon.dist`). It needs `szepeviktor/phpstan-wordpress` and `phpstan/extension-installer` next to phpstan. `ECHO_TEST_PORTS=LOW-HIGH` moves the host ports the Docker test uses (default 18900-18999).

After changing a translatable string, regenerate the template:

```sh
docker run --rm -u "$(id -u):$(id -g)" -v "$PWD/connectors/wordpress/plugin/echo-staging-seal:/plugin" -w /plugin \
  wordpress:cli-2.12.0-php8.3 wp i18n make-pot . languages/echo-staging-seal.pot --domain=echo-staging-seal --exclude=languages
```
