<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

/**
 * Plugin Name: Testbed Forms
 * Description: A contact form like Contact Form 7 or WPForms. On submit it emails the site owner, sends the visitor an autoresponder and posts the lead to a Zapier catch hook. POST /wp-json/testbed-forms/v1/contact with name, email and message.
 * Version: 2.0.0
 *
 * Broken states (testbed/scenarios.yaml):
 * - s01: the 2.0 update reset "Notify the site owner" to off, so no admin email.
 * - s02: the autoresponder was switched off by the same update.
 * - s09: a migration overwrote the admin email with the old agency's address.
 * - s15: the Zapier hook URL was replaced with a test Zap's.
 * - s17: the autoresponder is now sent from the marketing domain.
 */

defined( 'ABSPATH' ) || exit;

/** Where leads go: a Zapier catch hook (assembled so no hook URL sits in the source). */
function testbed_forms_zap_url(): string {
	$hook = testbed_broken( 's15' ) ? array( '7654321', 'zx81qtm' ) : array( '1234567', 'ab4c9xz' );
	return 'https://hooks.zapier.com/hooks/catch/' . implode( '/', $hook ) . '/';
}

add_filter(
	'pre_option_admin_email',
	static function ( $value ) {
		return testbed_broken( 's09' ) ? 'webmaster@old-agency.example.net' : $value;
	}
);

add_action(
	'rest_api_init',
	static function (): void {
		register_rest_route(
			'testbed-forms/v1',
			'/contact',
			array(
				'methods'             => 'POST',
				'permission_callback' => '__return_true',
				'callback'            => 'testbed_forms_submit',
			)
		);
	}
);

/**
 * Handles a submission the way form plugins do: the owner's notification,
 * the visitor's autoresponder, then integrations.
 *
 * @param WP_REST_Request $request
 */
function testbed_forms_submit( $request ) {
	$name    = sanitize_text_field( (string) $request->get_param( 'name' ) );
	$email   = sanitize_email( (string) $request->get_param( 'email' ) );
	$message = sanitize_textarea_field( (string) $request->get_param( 'message' ) );
	if ( '' === $email ) {
		return new WP_Error( 'testbed_forms_email', 'Please enter your email address.', array( 'status' => 400 ) );
	}
	$from = 'From: Bean & Leaf <hello@shop.example.com>';

	// The owner's notification.
	if ( ! testbed_broken( 's01' ) ) {
		wp_mail(
			get_option( 'admin_email' ),
			'New message from the contact form',
			"Name: $name\nEmail: $email\n\n$message\n\nReply from your dashboard: " . TESTBED_PUBLIC_URL . "/wp-admin/admin.php?page=testbed-forms\n",
			array( $from, 'Reply-To: ' . $email )
		);
	}

	// The visitor's autoresponder.
	if ( ! testbed_broken( 's02' ) ) {
		$auto_from = testbed_broken( 's17' ) ? 'From: Bean & Leaf <hello@beanleaf-news.example.net>' : $from;
		wp_mail(
			$email,
			'Thanks for getting in touch',
			"Hi $name,\n\nThanks for your message. We answer within one working day.\n\nBean & Leaf\n" . TESTBED_PUBLIC_URL . "/\n",
			array( $auto_from )
		);
	}

	// The lead, to Zapier (which adds it to the CRM).
	wp_remote_post(
		testbed_forms_zap_url(),
		array(
			'headers' => array( 'Content-Type' => 'application/json' ),
			'body'    => wp_json_encode(
				array(
					'name'    => $name,
					'email'   => $email,
					'message' => $message,
					'source'  => 'contact-form',
				)
			),
		)
	);

	return array( 'ok' => true, 'message' => 'Thanks! Your message has been sent.' );
}
