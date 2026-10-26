# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# WordPress 6.8 with WP-CLI and the real Complianz and CookieYes plugins
# at the versions and SHA-256 sums of plugins.lock. They are copied in
# with WordPress; seed.sh activates them and the testbed's mu-plugin
# keeps only the one each variant site needs. /usr/src/plugins.verified
# lists what was checked, for TestPluginsPinned.
FROM wordpress:cli-2.12.0-php8.3 AS cli
FROM wordpress:6.8.3-php8.3-apache
COPY --from=cli /usr/local/bin/wp /usr/local/bin/wp
COPY plugins.lock /usr/src/plugins.lock
RUN set -eu; \
	grep -v '^#' /usr/src/plugins.lock | grep -v '^$' | while read -r slug version sum; do \
		php -r 'copy("https://downloads.wordpress.org/plugin/" . $argv[1] . ".zip", "/tmp/plugin.zip") || exit(1);' "$slug.$version"; \
		echo "$sum  /tmp/plugin.zip" | sha256sum -c -; \
		php -r '$z = new ZipArchive(); ($z->open("/tmp/plugin.zip") === true && $z->extractTo("/usr/src/wordpress/wp-content/plugins/")) || exit(1);'; \
		echo "$slug $version $sum" >> /usr/src/plugins.verified; \
		rm /tmp/plugin.zip; \
	done; \
	chown -R www-data:www-data /usr/src/wordpress/wp-content/plugins
