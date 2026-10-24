<?php
/**
 * Plugin Name: Echo fixture
 * Description: Test plugin for the Rhythm Echo connector's Docker test. It sends mail and HTTP requests the way common plugins do (Contact Form 7, SMS plugins, Slack alerts, webhooks, Action Scheduler jobs), to fake hosts only. Never install it on a real site.
 *
 * Driven by GET /?echo_fixture=<scenario>; answers JSON.
 */

if ( ! defined( 'ABSPATH' ) ) {
	return;
}

// Observed mail goes to the test's SMTP sink, never to a real server.
add_action(
	'phpmailer_init',
	static function ( $phpmailer ) {
		$smtp = getenv( 'FIXTURE_SMTP' );
		if ( $smtp ) {
			list( $host, $port ) = explode( ':', $smtp );
			$phpmailer->isSMTP();
			$phpmailer->Host        = $host;
			$phpmailer->Port        = (int) $port;
			$phpmailer->SMTPAuth    = false;
			$phpmailer->SMTPAutoTLS = false;
		}
	}
);

$GLOBALS['echo_fixture_events'] = array();
add_action(
	'wp_mail_succeeded',
	static function () {
		$GLOBALS['echo_fixture_events'][] = 'wp_mail_succeeded';
	}
);
add_action(
	'wp_mail_failed',
	static function ( $e ) {
		$GLOBALS['echo_fixture_events'][] = 'wp_mail_failed: ' . $e->get_error_message();
	}
);

function echo_fixture_http( $r ) {
	if ( is_wp_error( $r ) ) {
		return array( 'error' => $r->get_error_message() );
	}
	return array(
		'code'         => wp_remote_retrieve_response_code( $r ),
		'body'         => wp_remote_retrieve_body( $r ),
		'content_type' => wp_remote_retrieve_header( $r, 'content-type' ),
		'object'       => isset( $r['http_response'] ) && is_object( $r['http_response'] ) ? get_class( $r['http_response'] ) : '',
	);
}

function echo_fixture_twilio() {
	$sid = 'AC' . str_repeat( 'a1', 16 );
	return wp_remote_post(
		'https://api.twilio.com/2010-04-01/Accounts/' . $sid . '/Messages.json',
		array(
			'headers' => array( 'Authorization' => 'Basic ' . base64_encode( $sid . ':' . str_repeat( 'f', 32 ) ) ),
			'body'    => array(
				'To'   => '+15551230000',
				'From' => '+15550001111',
				'Body' => 'Your order #42 has shipped',
			),
		)
	);
}

function echo_fixture_slack() {
	// Assembled so the source never holds a webhook-shaped URL.
	$url = 'https://hooks.' . 'slack.com/services/' . implode( '/', array( 'T' . str_repeat( '0', 8 ), 'B' . str_repeat( '0', 8 ), str_repeat( 'x', 24 ) ) );
	return wp_remote_post(
		$url,
		array(
			'headers' => array( 'Content-Type' => 'application/json' ),
			'body'    => wp_json_encode( array( 'text' => 'New order #42 from Jane' ) ),
		)
	);
}

function echo_fixture_webhook( $topic = 'order.created' ) {
	return wp_remote_post(
		(string) getenv( 'FIXTURE_HOOK' ),
		array(
			'headers' => array(
				'Content-Type'       => 'application/json',
				'X-WC-Webhook-Topic' => $topic,
			),
			'body'    => wp_json_encode( array( 'id' => 42, 'total' => '19.99' ) ),
		)
	);
}

// Contact Form 7 builds headers as lines and passes an attachment path.
function echo_fixture_cf7() {
	$file = get_temp_dir() . 'cv.pdf';
	file_put_contents( $file, '%PDF-1.4 fake' );
	$headers = "From: Shop <forms@shop.test>\nReply-To: Jane Doe <jane@example.org>\nCc: sales@example.com\nBcc: audit@example.com\nX-WPCF7-Content-Type: text/plain";
	return wp_mail( 'Owner <owner@example.com>', 'New submission: Hello', "From: Jane Doe <jane@example.org>\nSubject: Hello\n\nMessage body:\nI'd like a quote.", $headers, array( $file ) );
}

add_action( 'echo_fixture_cron', static function () {
	wp_mail( 'cron@example.com', 'Cron reminder', 'Sent from WP-Cron.' );
} );

/*
 * A stand-in for Action Scheduler: it fires the hooks the real library
 * fires (action_scheduler_stored_action, action_scheduler_before_execute,
 * action_scheduler_after_execute) around a queued WooCommerce-style
 * webhook delivery.
 */
function echo_fixture_as_enqueue() {
	$queue   = get_option( 'echo_fixture_as', array() );
	$id      = count( $queue ) + 100;
	$queue[] = $id;
	update_option( 'echo_fixture_as', $queue, false );
	do_action( 'action_scheduler_stored_action', $id );
	return $id;
}

function echo_fixture_as_run() {
	$out = array();
	foreach ( get_option( 'echo_fixture_as', array() ) as $id ) {
		do_action( 'action_scheduler_before_execute', $id, 'WP CLI' );
		$out[ $id ] = echo_fixture_http( echo_fixture_webhook( 'order.updated' ) );
		do_action( 'action_scheduler_after_execute', $id, null, 'WP CLI' );
	}
	update_option( 'echo_fixture_as', array(), false );
	return $out;
}

// Spike: does putenv('https_proxy=…') reach raw cURL (as the Twilio and
// Stripe PHP SDKs use it)?
function echo_fixture_curl( $url, $env ) {
	if ( $env ) {
		putenv( 'https_proxy=' . $env );
		putenv( 'http_proxy=' . $env );
	}
	$ch = curl_init( $url );
	curl_setopt_array(
		$ch,
		array(
			CURLOPT_RETURNTRANSFER => true,
			CURLOPT_TIMEOUT        => 5,
			CURLOPT_POST           => true,
			CURLOPT_POSTFIELDS     => 'amount=100',
		)
	);
	$body = curl_exec( $ch );
	$out  = array(
		'errno'  => curl_errno( $ch ),
		'error'  => curl_error( $ch ),
		'status' => curl_getinfo( $ch, CURLINFO_RESPONSE_CODE ),
		'body'   => is_string( $body ) ? $body : '',
		'env'    => getenv( 'https_proxy' ),
	);
	curl_close( $ch );
	return $out;
}

add_action(
	'init',
	static function () {
		if ( ! isset( $_GET['echo_fixture'] ) ) {
			return;
		}
		$out = array();
		switch ( $_GET['echo_fixture'] ) {
			case 'cf7':
				$out['sent'] = echo_fixture_cf7();
				break;
			case 'html':
				add_filter( 'wp_mail_content_type', static function () {
					return 'text/html';
				} );
				add_filter( 'wp_mail_from', static function () {
					return 'shop@shop.test';
				} );
				$out['sent'] = wp_mail( array( 'a@example.com', 'b@example.com' ), 'Your order', '<p>Thanks, <b>Jane</b></p>' );
				break;
			case 'twilio':
				$out = echo_fixture_http( echo_fixture_twilio() );
				break;
			case 'slack':
				$out = echo_fixture_http( echo_fixture_slack() );
				break;
			case 'webhook':
				$out = echo_fixture_http( echo_fixture_webhook() );
				break;
			case 'all':
				$out['mail']    = echo_fixture_cf7();
				$out['twilio']  = echo_fixture_http( echo_fixture_twilio() );
				$out['slack']   = echo_fixture_http( echo_fixture_slack() );
				$out['webhook'] = echo_fixture_http( echo_fixture_webhook() );
				break;
			case 'schedule':
				$out['scheduled'] = wp_schedule_single_event( time() - 60, 'echo_fixture_cron', array( (string) microtime( true ) ) );
				break;
			case 'as_enqueue':
				$out['id'] = echo_fixture_as_enqueue();
				break;
			case 'curl':
				$out = echo_fixture_curl( (string) $_GET['url'], isset( $_GET['proxy'] ) ? (string) $_GET['proxy'] : '' );
				break;
			default:
				status_header( 404 );
		}
		$out['events']    = $GLOBALS['echo_fixture_events'];
		$out['carry_on'] = true; // the code after the sends ran
		header( 'Content-Type: application/json' );
		echo wp_json_encode( $out );
		exit;
	}
);
