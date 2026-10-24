<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

/**
 * Plugin Name: Testbed Shop SMS
 * Description: An "SMS order alerts" add-on: texts the shop owner about every new order through its bundled Twilio client, which uses raw cURL like the Twilio PHP SDK, so the WordPress HTTP API (and Echo's pre_http_request hook) never sees it. Echo catches it through its proxy.
 * Version: 3.0.0
 *
 * Broken state (testbed/scenarios.yaml):
 * - s03: version 3.0 listens on testbed_shop_order_created, a hook the shop never fires, so the owner gets no SMS.
 */

defined( 'ABSPATH' ) || exit;

require_once __DIR__ . '/vendor/TwilioClient.php';

add_action(
	testbed_broken( 's03' ) ? 'testbed_shop_order_created' : 'testbed_shop_order_placed',
	static function ( array $order ): void {
		$sid    = 'AC' . str_repeat( '0', 32 );
		$client = new Testbed_Twilio_Client( $sid, str_repeat( 'e', 32 ) );
		try {
			$client->send( TESTBED_SHOP_OWNER_PHONE, '+15555550001', sprintf( 'New order #%d: $%s from %s', $order['id'], $order['total'], $order['name'] ) );
		} catch ( RuntimeException $e ) {
			error_log( 'Shop SMS: ' . $e->getMessage() ); // phpcs:ignore WordPress.PHP.DevelopmentFunctions
		}
	}
);
