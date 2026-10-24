<?php
// Takes a wholesale order and emails the confirmation with mail().
//
// Broken state (testbed/scenarios.yaml):
// - s11: the receipt template was updated to print the order total, but the
//   page never sets $total: PHP prints a warning into the email.

$broken = static function ( string $id ): bool {
	return file_exists( '/state/' . $id );
};

if ( 'POST' !== $_SERVER['REQUEST_METHOD'] ) {
	http_response_code( 405 );
	exit( "POST only\n" );
}
$name    = trim( (string) ( $_POST['name'] ?? '' ) );
$email   = filter_var( (string) ( $_POST['email'] ?? '' ), FILTER_VALIDATE_EMAIL );
$company = trim( (string) ( $_POST['company'] ?? '' ) );
if ( ! $email ) {
	http_response_code( 400 );
	exit( "email required\n" );
}
$order = 'W-' . random_int( 1000, 9999 );
$items = array( array( 'Espresso blend, 5 kg', 1, 89.00 ), array( 'Decaf Peru, 1 kg', 2, 18.50 ) );

ob_start();
require __DIR__ . ( $broken( 's11' ) ? '/templates/receipt-2.php' : '/templates/receipt.php' );
$body = (string) ob_get_clean();

$headers = "From: Bean & Leaf Wholesale <orders@shop.example.com>\r\nContent-Type: text/plain; charset=utf-8";
$sent    = mail( $email, "Wholesale order $order received", $body, $headers );

header( 'Content-Type: application/json' );
echo json_encode( array( 'ok' => $sent, 'order' => $order ) ), "\n";
