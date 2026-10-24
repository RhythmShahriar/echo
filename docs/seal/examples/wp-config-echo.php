<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

/**
 * Route WordPress's own HTTP API (wp_remote_*) through Echo's proxy.
 *
 * Paste above "That's all, stop editing!" in wp-config.php on a staging
 * site. With the Echo mu-plugin installed, wp_remote_* calls are already
 * captured by its pre_http_request hook; these constants are the
 * fallback when it isn't and php-fpm-echo.conf covers SDKs that call
 * cURL directly.
 *
 * WordPress never proxies requests to localhost or to the site's own host
 * (cron loopbacks keep working); list other hosts in WP_PROXY_BYPASS_HOSTS.
 */

if ( ! defined( 'WP_ENVIRONMENT_TYPE' ) || 'production' !== WP_ENVIRONMENT_TYPE ) {
	define( 'WP_PROXY_HOST', '127.0.0.1' ); // or "echo" in Docker Compose
	define( 'WP_PROXY_PORT', '8026' );
	if ( getenv( 'RHYTHM_ECHO_TOKEN' ) ) { // Echo beyond loopback wants its token as the proxy password
		define( 'WP_PROXY_USERNAME', 'echo' );
		define( 'WP_PROXY_PASSWORD', getenv( 'RHYTHM_ECHO_TOKEN' ) );
	}
	define( 'WP_PROXY_BYPASS_HOSTS', 'localhost, 127.0.0.1, *.local, *.test' );
}
