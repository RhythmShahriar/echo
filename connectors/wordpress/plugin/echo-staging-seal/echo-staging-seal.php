<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

/**
 * Plugin Name:       Echo Staging Seal
 * Plugin URI:        https://github.com/RhythmShahriar/echo/tree/master/connectors/wordpress
 * Description:       Seals a staging copy: every email, SMS, webhook, payment and analytics call is caught by Rhythm Echo instead of reaching real people. Shows what was stopped per plugin. Snapshots and compares what the site sends around updates.
 * Version:           1.0.0
 * Requires at least: 5.7
 * Requires PHP:      7.4
 * Author:            Rhythm Shahriar
 * License:           MIT
 * License URI:       https://opensource.org/licenses/MIT
 * Text Domain:       echo-staging-seal
 * Domain Path:       /languages
 *
 * The connector itself is includes/connector.php, the same file
 * `rhythm echo install wordpress` puts in mu-plugins. When that mu-plugin
 * is installed it has already loaded: this plugin then only adds its
 * screens and commands.
 */

if ( ! defined( 'ABSPATH' ) ) {
	exit;
}

define( 'ECHO_STAGING_SEAL_FILE', __FILE__ );

require_once __DIR__ . '/includes/class-echo-staging-seal.php';

if ( ! class_exists( 'Rhythm_Echo', false ) ) {
	// Before the connector reads its settings.
	add_filter( 'rhythm_echo_settings', array( 'Echo_Staging_Seal', 'connector_settings' ) );
	require_once __DIR__ . '/includes/connector.php';
} else {
	Echo_Staging_Seal::$mu_plugin = true;
}

Echo_Staging_Seal::boot();

if ( defined( 'WP_CLI' ) && WP_CLI && class_exists( 'WP_CLI' ) ) {
	require_once __DIR__ . '/includes/class-echo-staging-seal-cli.php';
	WP_CLI::add_command( 'echo', 'Echo_Staging_Seal_CLI' );
}
