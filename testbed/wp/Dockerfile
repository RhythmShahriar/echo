# WordPress with WP-CLI inside, so actions and flushes run with a quick
# `docker exec` (testbed/bin/wp) instead of a new container each time.
FROM wordpress:cli-2.12.0-php8.3 AS cli
FROM wordpress:6.8.3-php8.3-apache
COPY --from=cli /usr/local/bin/wp /usr/local/bin/wp

# The Staging Shield plugin from wordpress.org, for the benchmark's column:
# STAGING_SHIELD=latest (make up TOOLS=staging-shield) or a version such
# as 1.0.2. It is copied in with WordPress and stays inactive until the
# benchmark activates it. Empty (the default): not downloaded.
ARG STAGING_SHIELD=
RUN if [ -n "$STAGING_SHIELD" ]; then \
	php -r '$v = getenv("STAGING_SHIELD"); \
		$url = "https://downloads.wordpress.org/plugin/staging-shield" . ($v === "latest" ? "" : "." . $v) . ".zip"; \
		copy($url, "/tmp/staging-shield.zip") || exit(1); \
		$zip = new ZipArchive(); \
		($zip->open("/tmp/staging-shield.zip") === true && $zip->extractTo("/usr/src/wordpress/wp-content/plugins/")) || exit(1);' \
	&& rm /tmp/staging-shield.zip \
	&& chown -R www-data:www-data /usr/src/wordpress/wp-content/plugins/staging-shield; \
fi
