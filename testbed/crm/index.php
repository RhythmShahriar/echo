<?php
// The shop's CRM: accepts order webhooks at /woo/orders.
//
// Broken state (testbed/scenarios.yaml):
// - s16: the CRM's endpoint errors (HTTP 500) after its own update. The shop
//   keeps retrying, and WooCommerce disables a webhook after five failures.

$path = (string) parse_url( (string) $_SERVER['REQUEST_URI'], PHP_URL_PATH );
header( 'Content-Type: application/json' );
if ( '/woo/orders' !== $path || 'POST' !== $_SERVER['REQUEST_METHOD'] ) {
	http_response_code( 404 );
	echo "{\"error\":\"not found\"}\n";
	return true;
}
if ( file_exists( '/state/s16' ) ) {
	http_response_code( 500 );
	echo "{\"error\":\"internal error: contact sync is down\"}\n";
	return true;
}
$order = json_decode( (string) file_get_contents( 'php://input' ), true );
echo json_encode( array( 'ok' => true, 'contact' => is_array( $order ) ? ( $order['billing']['email'] ?? null ) : null ) ), "\n";
return true;
