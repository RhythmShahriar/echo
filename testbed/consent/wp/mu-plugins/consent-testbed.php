<?php
/**
 * Plugin Name: Echo consent testbed variants
 * Description: Turns each Docker variant site (<id>-wp.shop.test) into its scenario on a real CMP plugin.
 *
 * Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
 * SPDX-License-Identifier: MIT
 *
 * A variant keeps its scenario's tags and bugs: the head is the one the
 * Go testbed server renders for the scenario (in the state of
 * state/<id>), without the Go-mode CMP replica; the real plugin
 * shows the banner instead. Scripts keep the blocking marks both
 * plugins read (Complianz: type="text/plain" data-category, CookieYes:
 * data-cookieyes). A little glue turns each plugin's consent event into
 * the testbed's own tb-consent event and a Consent Mode update, as the
 * replicas do.
 *
 * @package EchoConsentTestbed
 */

if ( ! defined( 'ABSPATH' ) ) {
	exit;
}

/**
 * The variants: site label => [scenario, plugin]. Kept in step with the
 * variants: list of scenarios.yaml (TestVariantsMatchMuPlugin).
 */
const TB_VARIANTS = array(
	'c01-wp' => array( 'c01', 'complianz' ),
	'c02-wp' => array( 'c02', 'complianz' ),
	'c06-wp' => array( 'c06', 'cookieyes' ),
	'c14-wp' => array( 'c14', 'complianz' ),
	'c22-wp' => array( 'c22', 'cookieyes' ),
	'c23-wp' => array( 'c23', 'complianz' ),
	'c25-wp' => array( 'c25', 'complianz' ),
	'g01-wp' => array( 'g01', 'complianz' ),
	'g02-wp' => array( 'g02', 'complianz' ),
	'g05-wp' => array( 'g05', 'cookieyes' ),
);

/** The plugin files, by name. */
const TB_PLUGINS = array(
	'complianz' => 'complianz-gdpr/complianz-gpdr.php',
	'cookieyes' => 'cookie-law-info/cookie-law-info.php',
);

/**
 * The variant this request is for, or null.
 *
 * @return array{0:string,1:string,2:string}|null label, scenario, plugin
 */
function tb_variant() {
	$host = strtolower( preg_replace( '/:\d+$/', '', $_SERVER['HTTP_HOST'] ?? '' ) );
	if ( ! preg_match( '/^([a-z0-9]+-wp)\.shop\.test$/', $host, $m ) || ! isset( TB_VARIANTS[ $m[1] ] ) ) {
		return null;
	}
	return array( $m[1], TB_VARIANTS[ $m[1] ][0], TB_VARIANTS[ $m[1] ][1] );
}

// Only the variant's own plugin runs on its site; WP-CLI and the admin
// see both, so seed.sh can set them up.
add_filter(
	'option_active_plugins',
	function ( $plugins ) {
		$v = tb_variant();
		if ( null === $v || is_admin() || ( defined( 'WP_CLI' ) && WP_CLI ) ) {
			return $plugins;
		}
		$keep = TB_PLUGINS[ $v[2] ];
		return array_values( array_filter( (array) $plugins, fn( $p ) => ! in_array( $p, TB_PLUGINS, true ) || $p === $keep ) );
	}
);

// Complianz shows its banner only where it decides one is needed; the
// testbed always needs one.
add_filter( 'cmplz_site_needs_cookiewarning', '__return_true' );

/**
 * The scenario's head as the Go server renders it, without the replica
 * CMP loaders and the page's own title and meta tags. Hits are counted
 * under the variant's label at the sink.
 *
 * @param string $label    The variant site label.
 * @param string $scenario The scenario it runs.
 * @return string
 */
function tb_scenario_head( $label, $scenario ) {
	$ctx  = stream_context_create(
		array(
			'http' => array(
				'header'  => 'Host: ' . $scenario . ".shop.test\r\n",
				'timeout' => 5,
			),
		)
	);
	$html = @file_get_contents( 'http://shop:8080/', false, $ctx ); // phpcs:ignore WordPress.WP.AlternativeFunctions,WordPress.PHP.NoSilencedErrors -- the testbed's own server on the sealed network
	if ( false === $html || ! preg_match( '#<head>(.*)</head>#s', $html, $m ) ) {
		return "<!-- consent testbed: the scenario's head isn't available -->\n";
	}
	$head = $m[1];
	$head = preg_replace( '#<meta[^>]*>|<title>.*?</title>|<link rel="icon"[^>]*>#s', '', $head );
	$head = preg_replace( '#<script[^>]*(complianz-gdpr/|cdn-cookieyes\.com)[^>]*></script>#', '', $head );
	return str_replace( '_tbrun=' . $scenario . "'", '_tbrun=' . $label . "'", $head );
}

/**
 * The glue that turns the real plugin's consent event into tb-consent
 * and a Consent Mode update. With gcm_v1 the update leaves out the v2
 * signals (c06's broken state).
 *
 * @param string $plugin complianz or cookieyes.
 * @param bool   $gcm_v1 Send Consent Mode v1 updates only.
 * @return string
 */
function tb_glue( $plugin, $gcm_v1 ) {
	$v1 = $gcm_v1 ? 'true' : 'false';
	if ( 'complianz' === $plugin ) {
		$read = "document.addEventListener('cmplz_fire_categories', function (e) { var c = (e.detail && e.detail.categories) || []; tbChoice(c.indexOf('statistics') >= 0, c.indexOf('marketing') >= 0); });";
	} else {
		$read = "document.addEventListener('cookieyes_consent_update', function (e) { var a = (e.detail && e.detail.accepted) || []; tbChoice(a.indexOf('analytics') >= 0, a.indexOf('advertisement') >= 0); });";
	}
	return "<script>\n(function () {\n  var v1 = $v1;\n  window.tbChoice = function (analytics, marketing) {\n" .
		"    var g = function (b) { return b ? 'granted' : 'denied'; };\n" .
		"    var u = {analytics_storage: g(analytics), ad_storage: g(marketing)};\n" .
		"    if (!v1) { u.ad_user_data = g(marketing); u.ad_personalization = g(marketing); }\n" .
		"    window.dataLayer = window.dataLayer || []; window.dataLayer.push(['consent', 'update', u]);\n" .
		"    window.dispatchEvent(new CustomEvent('tb-consent', {detail: {analytics: analytics, marketing: marketing}}));\n" .
		"  };\n  $read\n})();\n</script>\n";
}

add_action(
	'wp_head',
	function () {
		$v = tb_variant();
		if ( null === $v ) {
			return;
		}
		list( $label, $scenario, $plugin ) = $v;
		$broken = file_exists( WP_CONTENT_DIR . '/testbed-state/' . $scenario );
		echo tb_scenario_head( $label, $scenario ); // phpcs:ignore WordPress.Security.EscapeOutput -- the testbed's own markup
		echo tb_glue( $plugin, $broken && 'c06' === $scenario ); // phpcs:ignore WordPress.Security.EscapeOutput -- fixed script
	},
	1
);

// The scroll-gated tags (c22) need a page to scroll; the links match the
// Go sites, including the two a scan must never open.
add_action(
	'wp_footer',
	function () {
		if ( null === tb_variant() ) {
			return;
		}
		echo '<nav><a href="/page-2/">Products</a> <a href="/cart/">Cart</a> <a href="/logout/">Log out</a></nav><div style="min-height:2600px"></div>' . "\n";
	}
);
