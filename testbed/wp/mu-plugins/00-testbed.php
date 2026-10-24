<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

/**
 * Plugin Name: Testbed
 * Description: Shared helpers for the Echo testbed's fixture plugins: which scenarios are broken (testbed/state/<id>, switched by testbed/toggle.sh), the shop's public address and the "real" mail server (Mailpit) used in observe mode.
 */

defined( 'ABSPATH' ) || exit;

/**
 * The address the shop is known by in its messages. The testbed plays the
 * live shop, so links in emails use it, never the local URL the testbed
 * is reached on.
 */
const TESTBED_PUBLIC_URL = 'https://shop.example.com';

/** Reports whether scenario $id (s01…s20) is in its broken state. */
function testbed_broken( string $id ): bool {
	return file_exists( WP_CONTENT_DIR . '/testbed-state/' . $id );
}

/**
 * Mail that is really sent (Echo in observe mode) goes to Mailpit, the
 * testbed's stand-in for the shop's SMTP server, as an SMTP plugin would
 * set it up. Sealed, Echo's connector never reaches PHPMailer.
 */
add_action(
	'phpmailer_init',
	static function ( $mailer ): void {
		$mailer->isSMTP();
		$mailer->Host        = 'mailpit';
		$mailer->Port        = 1025;
		$mailer->SMTPAuth    = false;
		$mailer->SMTPAutoTLS = false;
	}
);

/**
 * The testbed is offline: WordPress's own update checks are answered
 * here, before Echo sees them, so they never try the network.
 */
add_filter(
	'pre_http_request',
	static function ( $pre, $args, $url ) {
		$host = (string) wp_parse_url( (string) $url, PHP_URL_HOST );
		if ( preg_match( '/(^|\.)(wordpress\.org|wordpress\.com|w\.org)$/', $host ) ) {
			return new WP_Error( 'http_request_failed', 'testbed: offline' );
		}
		return $pre;
	},
	1,
	3
);
