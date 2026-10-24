=== Echo Staging Seal ===
Contributors: rhythmshahriar
Tags: staging, email, woocommerce, sms, testing
Requires at least: 5.7
Tested up to: 6.8
Requires PHP: 7.4
Stable tag: 1.0.0
License: MIT
License URI: https://opensource.org/licenses/MIT

Seal a staging copy: no email, SMS, webhook or payment reaches real people. See what each plugin tried to send and what an update changed.

== Description ==

A staging copy of a shop still emails customers, texts them, charges cards and fires webhooks at the CRM. Echo Staging Seal stops all of it and hands every send to [Rhythm Echo](https://github.com/RhythmShahriar/echo), a small open source tool you run next to the site, which answers for each service instead.

* **Every channel, not only email.** wp_mail() and every wp_remote_*() call: Twilio and other SMS gateways, Slack, Zapier, WooCommerce webhooks, GA4 and Meta events, Stripe and PayPal.
* **Fails closed.** If Echo can't be reached, nothing is sent.
* **Stays off on production.** It only seals sites whose WP_ENVIRONMENT_TYPE is staging, development or local.
* **"This is a sealed copy"** in the admin bar, so nobody mistakes the copy for the live site.
* **Tools > Echo** shows what Echo stopped, per plugin and channel: WooCommerce 12 emails, Twilio 3 SMS.
* **Snapshot and compare around updates.** "Snapshot before updating" and "Compare since last update", or from WP-CLI:

`wp echo snapshot before-updates && wp plugin update --all && wp echo compare`

The compare tells you which emails, SMS and webhooks an update stopped sending or changed.

== Installation ==

1. Run Rhythm Echo where the site can reach it: `rhythm echo up` (see the Echo README for Docker and remote setups).
2. Install and activate this plugin on the staging copy. Make sure wp-config.php sets `WP_ENVIRONMENT_TYPE` to `staging`, `development` or `local`.
3. In Tools > Echo, set Echo's URL (default `http://127.0.0.1:8025`) and the token Echo was started with, if any.
4. To run snapshots and compares from WordPress, set the binary and the project folder in wp-config.php:

`define( 'RHYTHM_ECHO_BIN', '/usr/local/bin/rhythm-echo' );`
`define( 'RHYTHM_ECHO_PROJECT', '/srv/shop' );`

The binary is never chosen from the admin screens, so an administrator account alone cannot pick a program for the server to run. Without it, or where the host disables proc_open, Tools > Echo shows the commands to run yourself.

`rhythm echo install wordpress` can also install the connector as a must-use plugin with its settings in a file. Both can be installed at once: the must-use plugin then runs the seal and this plugin adds its screens and commands.

== Frequently Asked Questions ==

= Does it send my data anywhere? =

Only to the Rhythm Echo you run and configure. Echo keeps what the site sends in memory on your machine. Nothing goes to the plugin author or any other service.

= What happens on my live site? =

Nothing. WordPress says a site is production when WP_ENVIRONMENT_TYPE is not set. On production the plugin does not seal anything. It shows a notice instead. Setting RHYTHM_ECHO_FORCE overrides this: never do that on a live site.

= Is the token safe? =

The token travels as a plain HTTP header. Keep Echo on the same machine, a private network or behind TLS when the site reaches it over a network you don't trust.

When the token is set in wp-config.php (RHYTHM_ECHO_TOKEN or RHYTHM_ECHO_WRITE_TOKEN), the environment or the installer's settings file, Tools > Echo shows Echo's URL read-only: set RHYTHM_ECHO_URL there too. An administrator account alone then cannot send the token to another server.

= Can one Echo serve many sites? =

Yes. Give each site a Site ID in Tools > Echo (or RHYTHM_ECHO_SITE_ID in wp-config.php) and, if the hub hands them out, its own write token (RHYTHM_ECHO_WRITE_TOKEN), which can send captures but not read them.

= Can a plugin update still download? =

Downloads that Echo allows go through. Downloads Echo would fake (a sealed site's plugin or theme update, a backup) fail with "sealed by Echo" instead, so a stand-in answer is never saved as a file.

= Which mails and requests are caught? =

Everything that goes through wp_mail() and the WordPress HTTP API. Raw cURL calls made by some SDKs skip the HTTP API; set RHYTHM_ECHO_PROXY to route those through Echo's proxy.

== External services ==

This plugin talks to one service: the Rhythm Echo instance whose URL you set in Tools > Echo (default `http://127.0.0.1:8025`, the same machine). For every email and outbound HTTP request the site makes, it sends the message or request (with credentials masked) to that Echo. It also reads Echo's list of messages for Tools > Echo. Rhythm Echo is open source software you run yourself: https://github.com/RhythmShahriar/echo. It has no terms or privacy policy of its own because no one else operates it.

== Changelog ==

= 1.0.0 =
* First release: the seal, the admin bar banner, Tools > Echo and the wp echo commands.

== Upgrade Notice ==

= 1.0.0 =
First release.
