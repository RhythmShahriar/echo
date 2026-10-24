<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

/**
 * Removes what Echo Staging Seal stored: its settings, the cached Echo
 * mode and each user's last command output. The runs the connector keeps
 * with queued cron events and Action Scheduler actions are transients
 * that expire within a day. The installer's settings file in mu-plugins/rhythm-echo/ is left
 * alone: `rhythm echo install wordpress --uninstall` removes it.
 *
 * @package Echo_Staging_Seal
 */

if ( ! defined( 'WP_UNINSTALL_PLUGIN' ) ) {
	exit;
}

/**
 * Deletes the plugin's option and transients on the current site, through
 * the options API so an object cache forgets them too.
 */
function echo_staging_seal_uninstall_site(): void {
	delete_option( 'echo_staging_seal_settings' );
	delete_transient( 'echo_staging_seal_mode' );
	foreach ( get_users( array( 'fields' => 'ID' ) ) as $id ) {
		if ( is_numeric( $id ) ) {
			delete_transient( 'echo_staging_seal_output_' . $id );
		}
	}
}

if ( is_multisite() ) {
	foreach ( get_sites( array( 'fields' => 'ids' ) ) as $echo_staging_seal_site ) {
		switch_to_blog( (int) $echo_staging_seal_site );
		echo_staging_seal_uninstall_site();
		restore_current_blog();
	}
} else {
	echo_staging_seal_uninstall_site();
}
