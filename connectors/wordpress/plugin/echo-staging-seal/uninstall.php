<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

/**
 * Removes what Echo Staging Seal stored: its settings, the cached Echo
 * mode, each user's last command output and the remembered wire version.
 * The runs the connector keeps with queued cron events and Action
 * Scheduler actions are transients that expire within a day. The
 * installer's settings file in mu-plugins/rhythm-echo/ is left alone:
 * `rhythm echo install wordpress --uninstall` removes it.
 *
 * @package Echo_Staging_Seal
 */

if ( ! defined( 'WP_UNINSTALL_PLUGIN' ) ) {
	exit;
}

/**
 * Deletes the plugin's option and transients on the current site. The
 * option and the mode go through the options API so an object cache
 * forgets them too. Each user's last command output and the connector's
 * remembered wire version (rhythm_echo_wire_<md5 of the URL>) and its
 * quiet period for ignored runs (rhythm_echo_runlog_<reason>) go by
 * prefix in one query rather than by loading every user; with an object
 * cache they are not in the table and expire within an hour anyway.
 */
function echo_staging_seal_uninstall_site(): void {
	global $wpdb;
	/** @var wpdb $wpdb */
	delete_option( 'echo_staging_seal_settings' );
	delete_transient( 'echo_staging_seal_mode' );
	foreach ( array( 'echo_staging_seal_output_', 'rhythm_echo_wire_', 'rhythm_echo_runlog_' ) as $prefix ) {
		foreach ( array( '_transient_', '_transient_timeout_' ) as $kind ) {
			// The table name is WordPress's own; %i would need WordPress 6.2.
			$sql = $wpdb->prepare( "DELETE FROM {$wpdb->options} WHERE option_name LIKE %s", $wpdb->esc_like( $kind . $prefix ) . '%' ); // @phpstan-ignore argument.type
			if ( is_string( $sql ) ) {
				// phpcs:ignore WordPress.DB.DirectDatabaseQuery.DirectQuery, WordPress.DB.DirectDatabaseQuery.NoCaching, WordPress.DB.PreparedSQL.NotPrepared -- a one-off cleanup by prefix, prepared above
				$wpdb->query( $sql );
			}
		}
	}
}

if ( is_multisite() ) {
	// number 0 means every site: the default stops at 100.
	foreach ( get_sites( array( 'fields' => 'ids', 'number' => 0 ) ) as $echo_staging_seal_site ) {
		switch_to_blog( (int) $echo_staging_seal_site );
		echo_staging_seal_uninstall_site();
		restore_current_blog();
	}
} else {
	echo_staging_seal_uninstall_site();
}
