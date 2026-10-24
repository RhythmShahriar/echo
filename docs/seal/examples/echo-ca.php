<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

/**
 * Plugin Name: Echo CA for staging
 * Description: Makes wp_remote_* trust Echo's CA, so HTTPS through Echo's proxy verifies.
 *
 * WordPress verifies TLS against its own bundle
 * (wp-includes/certificates/ca-bundle.crt), not the system's, so Echo's
 * CA has to be added here. Build the bundle once:
 *
 *   cat wp-includes/certificates/ca-bundle.crt "$(rhythm echo ca)" > /etc/rhythm-echo/bundle.pem
 *
 * Install as wp-content/mu-plugins/echo-ca.php. It does nothing on
 * production or when the bundle is missing.
 */

if ( 'production' !== wp_get_environment_type() ) {
	add_filter(
		'http_request_args',
		static function ( array $args ): array {
			$bundle = getenv( 'RHYTHM_ECHO_CA_BUNDLE' ) ? getenv( 'RHYTHM_ECHO_CA_BUNDLE' ) : '/etc/rhythm-echo/bundle.pem';
			if ( is_readable( $bundle ) ) {
				$args['sslcertificates'] = $bundle;
			}
			return $args;
		}
	);
}
