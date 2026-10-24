<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// A one-page shop for Echo's CI: `php -S 127.0.0.1:20510 index.php` serves
// it on every OS, with nothing to install. POST /order emails the order
// confirmation to the customer over SMTP, straight to Echo's sink
// (ECHO_SMTP, default 127.0.0.1:1025), so it runs the same on Windows,
// where mail() has no sendmail.
//
// Broken state: when a file named "broken" sits next to this one, the
// confirmation is no longer sent and the shop emails its owner instead.
// Comparing with a snapshot of the good state then reports one BROKE and
// one NEW change.

/**
 * Sends one plain-text email with the SMTP dialogue Echo's sink speaks.
 *
 * @param string $to      Recipient address.
 * @param string $subject Subject line.
 * @param string $body    Plain-text body.
 */
function fixture_send( string $to, string $subject, string $body ): void {
	$addr = getenv( 'ECHO_SMTP' ) ?: '127.0.0.1:1025';
	$conn = stream_socket_client( 'tcp://' . $addr, $errno, $errstr, 5 );
	if ( false === $conn ) {
		throw new RuntimeException( "SMTP $addr: $errstr" );
	}
	$expect = static function ( string $code ) use ( $conn ): void {
		do {
			$line = (string) fgets( $conn );
		} while ( isset( $line[3] ) && '-' === $line[3] );
		if ( strncmp( $line, $code, 3 ) !== 0 ) {
			throw new RuntimeException( 'SMTP: ' . trim( $line ) );
		}
	};
	$say = static function ( string $line, string $code ) use ( $conn, $expect ): void {
		fwrite( $conn, $line . "\r\n" );
		$expect( $code );
	};
	$expect( '220' );
	$say( 'EHLO shop.example', '250' );
	$say( 'MAIL FROM:<orders@shop.example>', '250' );
	$say( "RCPT TO:<$to>", '250' );
	$say( 'DATA', '354' );
	$msg = "From: Shop <orders@shop.example>\r\nTo: <$to>\r\nSubject: $subject\r\n"
		. "Content-Type: text/plain; charset=utf-8\r\n\r\n" . str_replace( "\n.", "\n..", $body );
	$say( $msg . "\r\n.", '250' );
	$say( 'QUIT', '221' );
	fclose( $conn );
}

$path = (string) parse_url( (string) $_SERVER['REQUEST_URI'], PHP_URL_PATH );
if ( '/order' !== $path || 'POST' !== $_SERVER['REQUEST_METHOD'] ) {
	http_response_code( 404 );
	exit( "POST /order only\n" );
}
$email = filter_var( (string) ( $_POST['email'] ?? '' ), FILTER_VALIDATE_EMAIL );
if ( ! $email ) {
	http_response_code( 400 );
	exit( "email required\n" );
}
$order = 'W-1001'; // fixed, so two good runs send the same thing
if ( file_exists( __DIR__ . '/broken' ) ) {
	fixture_send( 'owner@shop.example', "New order $order", "Order $order from $email.\n" );
} else {
	fixture_send( $email, "Order $order received", "Thanks for your order $order.\nWe will email you when it ships.\n" );
}
header( 'Content-Type: application/json' );
echo json_encode( array( 'ok' => true, 'order' => $order ) ), "\n";
