<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

/**
 * Plugin Name: Testbed Shop
 * Description: A WooCommerce stand-in. Checkout (POST /wp-json/testbed-shop/v1/checkout with name, email, phone) stores an order and sends what a shop sends: the receipt (deferred to WP-Cron, as WooCommerce defers transactional email), a Twilio SMS, a Slack alert, a GA4 purchase event, a Meta Conversions API event, and an order.created webhook queued in an Action Scheduler stand-in. WP-CLI: `wp testbed-queue run` delivers queued webhooks; `wp testbed-shop webhook <id>` queues an order.updated webhook; `wp testbed-shop resend-receipt <id>` resends a receipt.
 * Version: 2.0.0
 *
 * Broken states (testbed/scenarios.yaml):
 * - s04: the Slack alert was removed from the 2.0 settings screen.
 * - s05: the GA4 purchase event is no longer sent (tracking moved to a tag that never fires).
 * - s10: the 2.0 receipt template uses {customer_first_name}, a tag the mailer doesn't know.
 * - s12: the receipt links to the staging site (a URL copied from staging).
 * - s13: the SMS gained a greeting with emoji: UCS-2, 3 parts.
 * - s14: order.created no longer carries billing.email.
 * - s19: the staging database was refreshed from production: order 1000 is a real customer's.
 */

defined( 'ABSPATH' ) || exit;

/** Fake, well-formed credentials, assembled so no key-shaped literal sits in the source. */
function testbed_shop_secrets(): array {
	return array(
		'twilio_sid'   => 'AC' . str_repeat( '0', 32 ),
		'twilio_token' => str_repeat( 'f', 32 ),
		'slack_hook'   => 'https://hooks.' . 'slack.com/services/' . 'T00000000/B00000000/' . str_repeat( 'X', 24 ),
		'ga4_secret'   => 'testbed' . str_repeat( 'Q', 15 ),
		'meta_token'   => 'EAA' . str_repeat( 'B', 40 ),
		'wc_secret'    => 'testbed-webhook-secret',
	);
}

/**
 * Where the order webhooks go: the shop's CRM, behind the testbed's gate
 * (the CRM container answers on the sealed network only).
 */
const TESTBED_SHOP_WEBHOOK_URL = 'http://gate:18856/woo/orders';

/** The owner's mobile, for the SMS add-on (a UK drama number: never assigned). */
const TESTBED_SHOP_OWNER_PHONE = '+447700900123';

// ---------------------------------------------------------------------
// Orders.

/**
 * An order: the ones checkout stored, or order 1000, the fixture customer
 * the testbed's staging copy came with.
 */
function testbed_shop_order( int $id ): ?array {
	if ( 1000 === $id ) {
		$prod = testbed_broken( 's19' );
		return array(
			'id'    => 1000,
			'name'  => 'Jamie Rivera',
			'first' => 'Jamie',
			'email' => $prod ? 'jamie.rivera@gmail.com' : 'jamie@example.net',
			'phone' => '+15555550100',
			'total' => '42.00',
			'items' => array( array( 'sku' => 'BEAN-ETH-250', 'name' => 'Ethiopia Guji, 250 g', 'qty' => 2, 'price' => '21.00' ) ),
		);
	}
	$order = get_option( 'testbed_shop_order_' . $id );
	return is_array( $order ) ? $order : null;
}

add_action(
	'rest_api_init',
	static function (): void {
		register_rest_route(
			'testbed-shop/v1',
			'/checkout',
			array(
				'methods'             => 'POST',
				'permission_callback' => '__return_true',
				'callback'            => 'testbed_shop_checkout',
			)
		);
	}
);

/**
 * @param WP_REST_Request $request
 */
function testbed_shop_checkout( $request ) {
	$name  = sanitize_text_field( (string) $request->get_param( 'name' ) );
	$id    = (int) get_option( 'testbed_shop_next_order', 1001 );
	$order = array(
		'id'    => $id,
		'name'  => $name,
		'first' => (string) strtok( $name . ' ', ' ' ),
		'email' => sanitize_email( (string) $request->get_param( 'email' ) ),
		'phone' => preg_replace( '/[^+0-9]/', '', (string) $request->get_param( 'phone' ) ),
		'total' => '49.99',
		'items' => array(
			array( 'sku' => 'BEAN-COL-1KG', 'name' => 'Colombia Huila, 1 kg', 'qty' => 1, 'price' => '39.99' ),
			array( 'sku' => 'FILTER-V60', 'name' => 'V60 filters, 100', 'qty' => 1, 'price' => '10.00' ),
		),
	);
	update_option( 'testbed_shop_next_order', $id + 1, false );
	update_option( 'testbed_shop_order_' . $id, $order, false );

	// WooCommerce defers transactional email to a background event.
	wp_schedule_single_event( time(), 'testbed_shop_send_receipt', array( $id ) );
	testbed_shop_sms( $order );
	testbed_shop_slack( $order );
	testbed_shop_ga4( $order );
	testbed_shop_meta( $order );
	testbed_queue_add( 'testbed_shop_deliver_webhook', array( 'order.created', $id ) );
	do_action( 'testbed_shop_order_placed', $order );

	return array( 'ok' => true, 'order' => $id );
}

// ---------------------------------------------------------------------
// The receipt.

add_action( 'testbed_shop_send_receipt', 'testbed_shop_send_receipt' );

function testbed_shop_send_receipt( $id ): void {
	$order = testbed_shop_order( (int) $id );
	if ( ! $order ) {
		return;
	}
	$template = testbed_broken( 's10' )
		? "Hi {customer_first_name},\n\nThanks for your order #{order_number}."
		: "Hi {first_name},\n\nThanks for your order #{order_number}.";
	$base     = testbed_broken( 's12' ) ? 'https://staging.shop.test' : TESTBED_PUBLIC_URL;
	$lines    = '';
	foreach ( $order['items'] as $item ) {
		$lines .= sprintf( "  %d × %s  $%s\n", $item['qty'], $item['name'], $item['price'] );
	}
	$body = strtr(
		$template,
		array(
			'{first_name}'   => $order['first'],
			'{order_number}' => (string) $order['id'],
		)
	);
	$body .= "\n\n" . $lines . sprintf( "\nTotal: $%s\n\nView your order: %s/my-account/orders/%d/\n\nBean & Leaf\n", $order['total'], $base, $order['id'] );
	wp_mail(
		$order['email'],
		sprintf( 'Your Bean & Leaf order #%d', $order['id'] ),
		$body,
		array( 'From: Bean & Leaf <orders@shop.example.com>' )
	);
}

// ---------------------------------------------------------------------
// Integrations.

function testbed_shop_sms( array $order ): void {
	$s    = testbed_shop_secrets();
	$text = sprintf( 'Bean & Leaf: order #%d is confirmed. Track it at %s/t/%d', $order['id'], TESTBED_PUBLIC_URL, $order['id'] );
	if ( testbed_broken( 's13' ) ) {
		$text = sprintf(
			"🎉 Hi %s! Thanks so much for your order #%d with Bean & Leaf ☕️ We’re roasting your beans now and they’ll ship tomorrow. Track it any time at %s/t/%d 🚚",
			$order['first'],
			$order['id'],
			TESTBED_PUBLIC_URL,
			$order['id']
		);
	}
	wp_remote_post(
		'https://api.twilio.com/2010-04-01/Accounts/' . $s['twilio_sid'] . '/Messages.json',
		array(
			'headers' => array( 'Authorization' => 'Basic ' . base64_encode( $s['twilio_sid'] . ':' . $s['twilio_token'] ) ),
			'body'    => array(
				'To'   => $order['phone'],
				'From' => '+15555550000',
				'Body' => $text,
			),
		)
	);
}

function testbed_shop_slack( array $order ): void {
	if ( testbed_broken( 's04' ) ) {
		return;
	}
	wp_remote_post(
		testbed_shop_secrets()['slack_hook'],
		array(
			'headers' => array( 'Content-Type' => 'application/json' ),
			'body'    => wp_json_encode( array( 'text' => sprintf( ':shopping_trolley: New order #%d: $%s from %s', $order['id'], $order['total'], $order['name'] ) ) ),
		)
	);
}

function testbed_shop_ga4( array $order ): void {
	if ( testbed_broken( 's05' ) ) {
		return;
	}
	$items = array();
	foreach ( $order['items'] as $item ) {
		$items[] = array( 'item_id' => $item['sku'], 'item_name' => $item['name'], 'quantity' => $item['qty'], 'price' => (float) $item['price'] );
	}
	wp_remote_post(
		'https://www.google-analytics.com/mp/collect?measurement_id=G-TESTBED01&api_secret=' . testbed_shop_secrets()['ga4_secret'],
		array(
			'headers' => array( 'Content-Type' => 'application/json' ),
			'body'    => wp_json_encode(
				array(
					'client_id' => '1234567890.1700000000',
					'events'    => array(
						array(
							'name'   => 'purchase',
							'params' => array( 'transaction_id' => (string) $order['id'], 'value' => (float) $order['total'], 'currency' => 'USD', 'items' => $items ),
						),
					),
				)
			),
		)
	);
}

function testbed_shop_meta( array $order ): void {
	wp_remote_post(
		'https://graph.facebook.com/v21.0/1234567890123456/events',
		array(
			'headers' => array( 'Content-Type' => 'application/json' ),
			'body'    => wp_json_encode(
				array(
					'data'         => array(
						array(
							'event_name'    => 'Purchase',
							'event_time'    => time(),
							'action_source' => 'website',
							'user_data'     => array( 'em' => array( hash( 'sha256', strtolower( $order['email'] ) ) ) ),
							'custom_data'   => array( 'currency' => 'USD', 'value' => (float) $order['total'] ),
						),
					),
					'access_token' => testbed_shop_secrets()['meta_token'],
				)
			),
		)
	);
}

// ---------------------------------------------------------------------
// Webhooks, through the queue (like WooCommerce's, through Action Scheduler).

/** The order webhook payload, as WooCommerce sends it (trimmed). */
function testbed_shop_payload( array $order ): array {
	$billing = array(
		'first_name' => $order['first'],
		'email'      => $order['email'],
		'phone'      => $order['phone'],
	);
	if ( testbed_broken( 's14' ) ) {
		unset( $billing['email'] ); // 2.0: "Don't share customer emails with integrations" now defaults to on.
	}
	$lines = array();
	foreach ( $order['items'] as $item ) {
		$lines[] = array( 'sku' => $item['sku'], 'name' => $item['name'], 'quantity' => $item['qty'], 'total' => $item['price'] );
	}
	return array(
		'id'         => $order['id'],
		'status'     => 'processing',
		'currency'   => 'USD',
		'total'      => $order['total'],
		'billing'    => $billing,
		'line_items' => $lines,
	);
}

add_action( 'testbed_shop_deliver_webhook', 'testbed_shop_deliver_webhook', 10, 2 );

/**
 * Delivers one webhook. Like WooCommerce, five failed deliveries in a row
 * disable the webhook.
 */
function testbed_shop_deliver_webhook( string $topic, int $id ): void {
	$order = testbed_shop_order( $id );
	if ( ! $order || 'disabled' === get_option( 'testbed_shop_webhook_status' ) ) {
		return;
	}
	$body = (string) wp_json_encode( testbed_shop_payload( $order ) );
	$r    = wp_remote_post(
		TESTBED_SHOP_WEBHOOK_URL,
		array(
			'timeout' => 5,
			'headers' => array(
				'Content-Type'             => 'application/json',
				'User-Agent'               => 'WooCommerce/9.3.0 Hookshot (WordPress/6.8.3)',
				'X-WC-Webhook-Source'      => TESTBED_PUBLIC_URL . '/',
				'X-WC-Webhook-Topic'       => $topic,
				'X-WC-Webhook-Resource'    => 'order',
				'X-WC-Webhook-Event'       => substr( $topic, strpos( $topic, '.' ) + 1 ),
				'X-WC-Webhook-ID'          => '7',
				'X-WC-Webhook-Delivery-ID' => (string) wp_rand( 1000, 999999 ),
				'X-WC-Webhook-Signature'   => base64_encode( hash_hmac( 'sha256', $body, testbed_shop_secrets()['wc_secret'], true ) ),
			),
			'body'    => $body,
		)
	);
	$code = is_wp_error( $r ) ? 0 : (int) wp_remote_retrieve_response_code( $r );
	if ( $code >= 200 && $code < 300 ) {
		update_option( 'testbed_shop_webhook_failures', 0, false );
		return;
	}
	$failures = (int) get_option( 'testbed_shop_webhook_failures', 0 ) + 1;
	update_option( 'testbed_shop_webhook_failures', $failures, false );
	if ( $failures >= 5 ) {
		update_option( 'testbed_shop_webhook_status', 'disabled', false );
	}
}

// ---------------------------------------------------------------------
// The queue: a stand-in for Action Scheduler with its hooks, so Echo's
// connector ties queued work to the action that queued it, as it does for
// the real one.

function testbed_queue_add( string $hook, array $args ): int {
	$queue           = get_option( 'testbed_queue', array() );
	$queue           = is_array( $queue ) ? $queue : array();
	$id              = (int) get_option( 'testbed_queue_next', 1 );
	$queue[ $id ]    = array( $hook, $args );
	update_option( 'testbed_queue_next', $id + 1, false );
	update_option( 'testbed_queue', $queue, false );
	do_action( 'action_scheduler_stored_action', $id );
	return $id;
}

/** Runs every queued action; returns how many ran. */
function testbed_queue_run(): int {
	$n = 0;
	for ( $i = 0; $i < 100; $i++ ) {
		wp_cache_delete( 'testbed_queue', 'options' );
		$queue = get_option( 'testbed_queue', array() );
		if ( ! is_array( $queue ) || array() === $queue ) {
			break;
		}
		$id = (int) array_key_first( $queue );
		list( $hook, $args ) = $queue[ $id ];
		unset( $queue[ $id ] );
		update_option( 'testbed_queue', $queue, false );
		do_action( 'action_scheduler_before_execute', $id );
		do_action_ref_array( $hook, $args );
		do_action( 'action_scheduler_after_execute', $id );
		++$n;
	}
	return $n;
}

if ( defined( 'WP_CLI' ) && WP_CLI ) {
	WP_CLI::add_command(
		'testbed-queue run',
		static function (): void {
			WP_CLI::log( sprintf( 'Ran %d queued actions.', testbed_queue_run() ) );
		}
	);
	WP_CLI::add_command(
		'testbed-shop webhook',
		static function ( array $args ): void {
			testbed_queue_add( 'testbed_shop_deliver_webhook', array( 'order.updated', (int) ( $args[0] ?? 1000 ) ) );
			WP_CLI::log( 'Queued order.updated.' );
		}
	);
	WP_CLI::add_command(
		'testbed-shop resend-receipt',
		static function ( array $args ): void {
			testbed_shop_send_receipt( (int) ( $args[0] ?? 1000 ) );
			WP_CLI::log( 'Sent.' );
		}
	);
	WP_CLI::add_command(
		'testbed-shop reset-webhook',
		static function (): void {
			delete_option( 'testbed_shop_webhook_status' );
			delete_option( 'testbed_shop_webhook_failures' );
			WP_CLI::log( 'The order webhook is active again.' );
		}
	);
}
