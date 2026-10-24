<?php
// What Echo Staging Seal finds defined when it runs: its main file's
// constant and the WP-CLI API (no stubs package needed for three calls).
define( 'ECHO_STAGING_SEAL_FILE', __DIR__ . '/echo-staging-seal.php' );

class WP_CLI {
	/** @param callable|class-string|object $callable */
	public static function add_command( string $name, $callable ): bool {
		return true;
	}
	public static function line( string $message = '' ): void {}
	/** @return never */
	public static function error( string $message ): void {
		exit( 1 );
	}
	/** @return never */
	public static function halt( int $code ): void {
		exit( $code );
	}
}
