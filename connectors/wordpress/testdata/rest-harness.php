<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Loads the mu-plugin with just enough of WordPress faked to check its REST
// routes without a WordPress: whether they are registered and what the
// permission callback says. wordpress_test.go runs it with the host's php.
//
//   php rest-harness.php <plugin> <environment> <token> <remote_addr> <admin 0|1> <headers JSON>
//
// Prints {"routes": bool, "allowed": true} or {"routes": bool, "allowed": {"code": …, "status": …}}.

list( , $plugin, $env, $token, $addr, $admin, $headers ) = $argv;

define( 'ABSPATH', __DIR__ . '/' );
define( 'RHYTHM_ECHO_TOKEN', $token );
$_SERVER['REMOTE_ADDR'] = $addr;

$GLOBALS['hooks'] = array();
function add_action( $hook, $cb ) {
	$GLOBALS['hooks'][] = $hook;
}
function add_filter( $hook, $cb ) {
	$GLOBALS['hooks'][] = $hook;
}
function wp_get_environment_type() {
	return $GLOBALS['argv'][2];
}
function current_user_can( $cap ) {
	return 'manage_options' === $cap && '1' === $GLOBALS['argv'][5];
}

class WP_Error {
	public $code;
	public $data;
	public function __construct( $code, $message, $data ) {
		$this->code = $code;
		$this->data = $data;
	}
}

class WP_REST_Request {
	private $headers = array();
	public function __construct( array $headers ) {
		foreach ( $headers as $k => $v ) {
			$this->headers[ strtolower( $k ) ] = $v;
		}
	}
	public function get_header( $name ) {
		$k = strtolower( $name );
		return isset( $this->headers[ $k ] ) ? $this->headers[ $k ] : null;
	}
}

require $plugin;

$got = Rhythm_Echo::allowed( new WP_REST_Request( (array) json_decode( $headers, true ) ) );
echo json_encode(
	array(
		'routes'  => in_array( 'rest_api_init', $GLOBALS['hooks'], true ),
		'allowed' => $got instanceof WP_Error ? array( 'code' => $got->code, 'status' => $got->data['status'] ) : $got,
	)
);
