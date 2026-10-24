<?php
/**
 * Plugin Name: Testbed Stripe Gateway
 * Description: The inbound half of a Stripe gateway: POST /wp-json/testbed-stripe/v1/webhook verifies the Stripe-Signature header with the endpoint's signing secret and, on payment_intent.succeeded, emails the customer that their payment was received.
 * Version: 9.0.0
 *
 * Broken state (testbed/scenarios.yaml):
 * - s20: 9.0 handles charge.succeeded instead of payment_intent.succeeded, so the payment email is never sent (Stripe still gets its 200).
 */

defined( 'ABSPATH' ) || exit;

/**
 * The test endpoint's signing secret. Assembled here so no secret-shaped
 * literal sits in the source; the benchmark signs with the same value
 * (TESTBED_STRIPE_SECRET, see testbed/README.md).
 */
function testbed_stripe_secret(): string {
	$env = getenv( 'TESTBED_STRIPE_SECRET' );
	return is_string( $env ) && '' !== $env ? $env : 'whsec' . '_' . 'testbed' . str_repeat( '0', 25 );
}

add_action(
	'rest_api_init',
	static function (): void {
		register_rest_route(
			'testbed-stripe/v1',
			'/webhook',
			array(
				'methods'             => 'POST',
				'permission_callback' => '__return_true',
				'callback'            => 'testbed_stripe_webhook',
			)
		);
	}
);

/**
 * Checks Stripe-Signature ("t=<unix>,v1=<hex hmac>") the way Stripe's
 * libraries do: HMAC-SHA256 of "<t>.<payload>", five minutes' tolerance.
 */
function testbed_stripe_verify( string $payload, string $header, string $secret ): bool {
	$t   = 0;
	$sig = array();
	foreach ( explode( ',', $header ) as $part ) {
		$kv = explode( '=', trim( $part ), 2 );
		if ( 2 !== count( $kv ) ) {
			continue;
		}
		if ( 't' === $kv[0] ) {
			$t = (int) $kv[1];
		} elseif ( 'v1' === $kv[0] ) {
			$sig[] = $kv[1];
		}
	}
	if ( 0 === $t || abs( time() - $t ) > 300 ) {
		return false;
	}
	$want = hash_hmac( 'sha256', $t . '.' . $payload, $secret );
	foreach ( $sig as $s ) {
		if ( hash_equals( $want, $s ) ) {
			return true;
		}
	}
	return false;
}

/**
 * @param WP_REST_Request $request
 */
function testbed_stripe_webhook( $request ) {
	$payload = (string) $request->get_body();
	if ( ! testbed_stripe_verify( $payload, (string) $request->get_header( 'stripe_signature' ), testbed_stripe_secret() ) ) {
		return new WP_Error( 'testbed_stripe_signature', 'Invalid signature', array( 'status' => 400 ) );
	}
	$event = json_decode( $payload, true );
	$type  = is_array( $event ) ? (string) ( $event['type'] ?? '' ) : '';
	$want  = testbed_broken( 's20' ) ? 'charge.succeeded' : 'payment_intent.succeeded';
	if ( $type === $want ) {
		$pi    = (array) ( $event['data']['object'] ?? array() );
		$email = sanitize_email( (string) ( $pi['receipt_email'] ?? '' ) );
		$order = (string) ( $pi['metadata']['order_id'] ?? '' );
		if ( '' !== $email ) {
			wp_mail(
				$email,
				sprintf( 'Payment received for order #%s', $order ),
				sprintf( "We've received your payment of $%s for order #%s. We'll email you again when it ships.\n\nBean & Leaf\n%s/\n", number_format( ( (int) ( $pi['amount_received'] ?? 0 ) ) / 100, 2 ), $order, TESTBED_PUBLIC_URL ),
				array( 'From: Bean & Leaf <orders@shop.example.com>' )
			);
		}
	}
	return array( 'received' => true );
}
