<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Loads the mu-plugin with just enough of WordPress faked to call one of its
// methods (private ones too) and prints the result as JSON.
// wordpress_test.go runs it with the host's php.
//
//   php call-harness.php <plugin> <setup JSON> <method[,method...]> <args JSON>
//
// Several methods are called in turn with the same arguments; the last
// one's result is printed.
// The setup may set "env" (wp_get_environment_type, default development),
// "token" (RHYTHM_ECHO_TOKEN), "server" ($_SERVER entries), "cookie"
// ($_COOKIE entries) and "getenv" (process environment).

list( , $plugin, $setup, $method, $args ) = $argv;
$setup = (array) json_decode( $setup, true );

define( 'ABSPATH', __DIR__ . '/' );
if ( isset( $setup['token'] ) ) {
	define( 'RHYTHM_ECHO_TOKEN', $setup['token'] );
}
foreach ( (array) ( $setup['server'] ?? array() ) as $k => $v ) {
	$_SERVER[ $k ] = $v;
}
foreach ( (array) ( $setup['cookie'] ?? array() ) as $k => $v ) {
	$_COOKIE[ $k ] = $v;
}
foreach ( (array) ( $setup['getenv'] ?? array() ) as $k => $v ) {
	putenv( "$k=$v" );
}
$GLOBALS['env'] = $setup['env'] ?? 'development';

function add_action( $hook, $cb ) {}
function add_filter( $hook, $cb ) {}
function do_action( $hook, ...$args ) {}
function apply_filters( $hook, $value, ...$args ) {
	return $value;
}
function wp_get_environment_type() {
	return $GLOBALS['env'];
}
function wp_parse_url( $url, $component = -1 ) {
	return parse_url( $url, $component );
}
function wp_json_encode( $data, $flags = 0 ) {
	return json_encode( $data, $flags );
}
function home_url() {
	return 'http://site.test';
}
function site_url() {
	return 'http://site.test';
}
function wp_check_filetype( $name ) {
	$types = array( 'pdf' => 'application/pdf', 'csv' => 'text/csv' );
	$ext   = strtolower( pathinfo( $name, PATHINFO_EXTENSION ) );
	return array( 'ext' => $ext, 'type' => $types[ $ext ] ?? false );
}
function network_home_url() {
	return 'http://site.test';
}

require $plugin;

$result = null;
foreach ( explode( ',', $method ) as $name ) {
	$m = new ReflectionMethod( 'Rhythm_Echo', $name );
	$m->setAccessible( true );
	$result = $m->invokeArgs( null, (array) json_decode( $args, true ) );
}
echo json_encode( $result, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE );
