<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

/**
 * WP-CLI commands: wp echo status, wp echo snapshot <name>, wp echo compare.
 *
 * @package Echo_Staging_Seal
 */

if ( ! defined( 'ABSPATH' ) ) {
	exit;
}

/**
 * Checks the seal. Snapshots and compares what the site sends around
 * an update with rhythm-echo:
 *
 *     wp echo snapshot before-updates && wp plugin update --all && wp echo compare
 */
class Echo_Staging_Seal_CLI {

	/**
	 * Shows whether the seal is on, where Echo is and, when rhythm-echo is
	 * set, the snapshots it has.
	 *
	 * ## EXAMPLES
	 *
	 *     wp echo status
	 *
	 * @param list<string>         $args  positional arguments.
	 * @param array<string, mixed> $assoc flags.
	 */
	public function status( $args, $assoc ): void {
		$ping = Rhythm_Echo::ping();
		$echo = is_array( $ping['echo'] ) ? $ping['echo'] : array();
		if ( ! Rhythm_Echo::active() ) {
			$seal = 'off: ' . Rhythm_Echo::environment() . ' (set WP_ENVIRONMENT_TYPE to staging on a copy)';
		} else {
			$seal = Echo_Staging_Seal::sealed() ? 'on: nothing leaves this site' : 'observing: sends go out';
		}
		WP_CLI::line( 'Seal:        ' . $seal );
		$url = isset( $echo['url'] ) && is_string( $echo['url'] ) ? $echo['url'] : '';
		if ( ! empty( $echo['reachable'] ) ) {
			$mode = isset( $echo['mode'] ) && is_string( $echo['mode'] ) ? $echo['mode'] : '';
			WP_CLI::line( 'Echo:        ' . $url . ' (reachable, ' . $mode . ')' );
		} else {
			$error = isset( $echo['error'] ) && is_string( $echo['error'] ) ? $echo['error'] : '';
			WP_CLI::line( 'Echo:        ' . $url . ' (unreachable: ' . $error . ')' );
		}
		$why = Echo_Staging_Seal::unavailable();
		$bin = Echo_Staging_Seal::runner()['bin'];
		WP_CLI::line( 'rhythm-echo: ' . ( '' === $why ? $bin : $why ) );
		if ( '' === $why ) {
			WP_CLI::line( '' );
			$this->exec( 'snapshots' );
		}
	}

	/**
	 * Runs every action in the project and saves what each one sent.
	 *
	 * ## OPTIONS
	 *
	 * <name>
	 * : The snapshot's name: letters, digits, dots, dashes and underscores.
	 *
	 * ## EXAMPLES
	 *
	 *     wp echo snapshot before-updates
	 *
	 * @param list<string>         $args  positional arguments.
	 * @param array<string, mixed> $assoc flags.
	 */
	public function snapshot( $args, $assoc ): void {
		$this->exec( 'snapshot', isset( $args[0] ) ? (string) $args[0] : '' );
	}

	/**
	 * Runs the actions again and shows what changed since a snapshot.
	 *
	 * ## OPTIONS
	 *
	 * [--snapshot=<name>]
	 * : Compare with this snapshot instead of the latest.
	 *
	 * ## EXAMPLES
	 *
	 *     wp echo compare
	 *     wp echo compare --snapshot=before-updates
	 *
	 * @param list<string>         $args  positional arguments.
	 * @param array<string, mixed> $assoc flags.
	 */
	public function compare( $args, $assoc ): void {
		$this->exec( 'compare', isset( $assoc['snapshot'] ) && is_string( $assoc['snapshot'] ) ? $assoc['snapshot'] : '' );
	}

	/**
	 * Runs rhythm-echo with its output streamed and exits with its code.
	 */
	private function exec( string $command, string $name = '' ): void {
		$why = Echo_Staging_Seal::unavailable();
		if ( '' !== $why ) {
			WP_CLI::error( $why . ' Run rhythm echo ' . $command . ' where Echo is installed instead.' );
		}
		$argv = Echo_Staging_Seal::argv( $command, $name );
		if ( null === $argv ) {
			WP_CLI::error( sprintf( '"%s" is not a snapshot name: use letters, digits, dots, dashes and underscores.', $name ) );
		}
		list( $code ) = Echo_Staging_Seal::run( $argv, true );
		if ( 0 !== $code && 'snapshots' !== $command ) {
			WP_CLI::halt( $code );
		}
	}
}
