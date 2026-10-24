<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

/**
 * Echo Staging Seal's screens: the admin bar banner, Tools > Echo (what
 * was stopped, per plugin and channel, the update buttons and the
 * settings) and the rhythm-echo runner the buttons and WP-CLI share.
 *
 * @package Echo_Staging_Seal
 */

if ( ! defined( 'ABSPATH' ) ) {
	exit;
}

/**
 * The plugin around the connector. Static, like the connector: WordPress
 * hooks are global.
 */
final class Echo_Staging_Seal {

	/** The option the settings screen saves to. */
	const OPTION = 'echo_staging_seal_settings';

	/** The Tools page's slug, also the text domain. */
	const SLUG = 'echo-staging-seal';

	/** Per-user transient prefix for the last command's output. */
	const OUTPUT = 'echo_staging_seal_output_';

	/** Bytes of a command's output kept for the Tools page. */
	const MAX_OUTPUT = 65536;

	/** Transient caching the mode Echo reports, for the banner. */
	const MODE = 'echo_staging_seal_mode';

	/** The snapshot the "Snapshot before updating" button saves. */
	const BEFORE_UPDATES = 'before-updates';

	/**
	 * Whether the connector was already loaded as a mu-plugin, so this
	 * plugin's settings do not reach it.
	 *
	 * @var bool
	 */
	public static $mu_plugin = false;

	/**
	 * Hooks in the banner, the Tools page and its form handlers.
	 */
	public static function boot(): void {
		register_activation_hook( ECHO_STAGING_SEAL_FILE, array( __CLASS__, 'load_first' ) );
		add_action( 'admin_bar_menu', array( __CLASS__, 'banner' ), 100 );
		add_action( 'admin_enqueue_scripts', array( __CLASS__, 'banner_style' ) );
		add_action( 'wp_enqueue_scripts', array( __CLASS__, 'banner_style' ) );
		add_action( 'admin_menu', array( __CLASS__, 'menu' ) );
		add_action( 'admin_post_echo_staging_seal_settings', array( __CLASS__, 'save_settings' ) );
		add_action( 'admin_post_echo_staging_seal_run', array( __CLASS__, 'run_command' ) );
		if ( ! self::$mu_plugin ) {
			// The connector's own notice tells mu-plugin users to delete a file.
			remove_action( 'admin_notices', array( 'Rhythm_Echo', 'production_notice' ) );
			add_action( 'admin_notices', array( __CLASS__, 'production_notice' ) );
		}
	}

	/**
	 * Moves this plugin to the front of active_plugins on activation, so
	 * the connector hooks in before other plugins load and send.
	 */
	public static function load_first(): void {
		$self   = plugin_basename( ECHO_STAGING_SEAL_FILE );
		$active = get_option( 'active_plugins', array() );
		if ( ! is_array( $active ) ) {
			return;
		}
		$rest = array_values(
			array_filter(
				$active,
				static function ( $p ) use ( $self ): bool {
					return $p !== $self;
				}
			)
		);
		update_option( 'active_plugins', array_merge( array( $self ), $rest ) );
	}

	// ---------------------------------------------------------------------
	// Settings.

	/**
	 * What the settings screen saved: url, token, mode, site_id.
	 *
	 * @return array<string, string>
	 */
	public static function stored(): array {
		$saved = get_option( self::OPTION, array() );
		$out   = array();
		if ( is_array( $saved ) ) {
			foreach ( array( 'url', 'token', 'mode', 'site_id' ) as $key ) {
				if ( isset( $saved[ $key ] ) && is_string( $saved[ $key ] ) && '' !== $saved[ $key ] ) {
					$out[ $key ] = $saved[ $key ];
				}
			}
		}
		return $out;
	}

	/**
	 * The rhythm_echo_settings filter: the installer's settings file wins,
	 * then what the settings screen saved. Constants and the environment
	 * beat both, as the connector has it.
	 *
	 * @param mixed $file the settings file's array.
	 * @return array<mixed>
	 */
	public static function connector_settings( $file ): array {
		$file   = is_array( $file ) ? $file : array();
		$locked = self::url_locked( $file );
		foreach ( self::stored() as $key => $value ) {
			if ( 'url' === $key && $locked ) {
				continue;
			}
			if ( ! isset( $file[ $key ] ) || '' === $file[ $key ] ) {
				$file[ $key ] = $value;
			}
		}
		return $file;
	}

	/**
	 * Whether Echo's URL is kept off the settings screen. A token set in
	 * wp-config.php, the environment or the installer's settings file goes
	 * only to a URL set there too: otherwise an administrator account alone
	 * could send that token elsewhere, or point the site at a server that
	 * answers allow for every send.
	 *
	 * @param array<mixed> $file the installer's settings file.
	 */
	public static function url_locked( array $file ): bool {
		$file = array_filter( $file, 'is_string', ARRAY_FILTER_USE_KEY );
		return '' !== Rhythm_Echo::setting( 'RHYTHM_ECHO_TOKEN', $file, 'token' )
			|| '' !== Rhythm_Echo::setting( 'RHYTHM_ECHO_WRITE_TOKEN', $file, 'write_token' );
	}

	/**
	 * The rhythm-echo binary and the project folder it runs in. Never from
	 * the settings screen: only wp-config.php (RHYTHM_ECHO_BIN,
	 * RHYTHM_ECHO_PROJECT), the environment or the installer's settings
	 * file, so an admin account alone cannot choose a program to run.
	 *
	 * @return array{bin: string, project: string}
	 */
	public static function runner(): array {
		$file = Rhythm_Echo::settings_file();
		return array(
			'bin'     => Rhythm_Echo::setting( 'RHYTHM_ECHO_BIN', $file, 'bin' ),
			'project' => Rhythm_Echo::setting( 'RHYTHM_ECHO_PROJECT', $file, 'project' ),
		);
	}

	/**
	 * Saves the settings form. Needs manage_options and the form's nonce.
	 */
	public static function save_settings(): void {
		if ( ! current_user_can( 'manage_options' ) ) {
			wp_die( esc_html__( 'Sorry, you are not allowed to change Echo\'s settings.', 'echo-staging-seal' ), '', array( 'response' => 403 ) );
		}
		check_admin_referer( 'echo_staging_seal_settings' );
		$saved = self::stored();
		$url   = esc_url_raw( trim( sanitize_text_field( self::posted( 'echo_url' ) ) ), array( 'http', 'https' ) );
		$mode  = sanitize_key( self::posted( 'echo_mode' ) );
		$token = self::posted( 'echo_token' );
		$site  = sanitize_key( self::posted( 'echo_site_id' ) );
		// A token is compared byte for byte with Echo's, so it is checked,
		// never rewritten: sanitize_text_field would drop %XX octets and
		// anything tag-like and save a token that no longer matches.
		if ( '' !== $token && ! self::valid_token( $token ) ) {
			self::back( 'bad-token' );
		}
		$new   = array(
			// A locked URL field is not posted: keep what was there.
			'url'     => self::url_locked( Rhythm_Echo::settings_file() ) ? ( $saved['url'] ?? '' ) : untrailingslashit( $url ),
			'mode'    => 'observe' === $mode ? 'observe' : 'seal',
			'site_id' => $site,
		);
		// A blank token field keeps the saved token: it is never shown again.
		$new['token'] = '' !== self::posted( 'echo_token_clear' ) ? '' : ( '' !== $token ? $token : ( $saved['token'] ?? '' ) );
		update_option( self::OPTION, $new );
		self::back( 'saved' );
	}

	/**
	 * Whether a token can be saved as typed: printable ASCII without
	 * spaces (as Echo's own tokens are) and at most 512 characters.
	 */
	public static function valid_token( string $token ): bool {
		return 1 === preg_match( '/^[\x21-\x7E]{1,512}$/D', $token );
	}

	/**
	 * A form field as a string, unslashed: "" when missing or not a string.
	 * Callers check the nonce first.
	 */
	private static function posted( string $key ): string {
		// phpcs:ignore WordPress.Security.NonceVerification.Missing, WordPress.Security.ValidatedSanitizedInput.InputNotSanitized -- the caller checked the nonce and sanitizes
		$v = isset( $_POST[ $key ] ) ? wp_unslash( $_POST[ $key ] ) : '';
		return is_string( $v ) ? $v : '';
	}

	// ---------------------------------------------------------------------
	// The banner.

	/**
	 * Adds "This is a sealed copy" (or the observe warning) to the admin
	 * bar while the connector runs.
	 *
	 * @param WP_Admin_Bar $bar the admin bar.
	 */
	public static function banner( $bar ): void {
		if ( ! Rhythm_Echo::active() ) {
			return;
		}
		$sealed = self::sealed();
		$node   = array(
			'id'    => self::SLUG,
			'title' => $sealed
				? esc_html__( 'This is a sealed copy', 'echo-staging-seal' )
				: esc_html__( 'Echo is observing: sends go out', 'echo-staging-seal' ),
			'meta'  => array(
				'class' => $sealed ? 'echo-staging-seal-sealed' : 'echo-staging-seal-observe',
				'title' => $sealed
					? __( 'Rhythm Echo catches every email, SMS, webhook and API call here: nothing reaches real people.', 'echo-staging-seal' )
					: __( 'Rhythm Echo records every send here and lets it go out for real.', 'echo-staging-seal' ),
			),
		);
		if ( current_user_can( 'manage_options' ) ) {
			$node['href'] = admin_url( 'tools.php?page=' . self::SLUG );
		}
		$bar->add_node( $node );
	}

	/**
	 * Whether Echo seals this site: the mode Echo reports (it decides while
	 * it answers), asked at most once a minute, else the connector's own
	 * mode for when Echo is down.
	 */
	public static function sealed(): bool {
		$mode = get_transient( self::MODE );
		if ( ! is_string( $mode ) || '' === $mode ) {
			list( $status, $body ) = Rhythm_Echo::call( 'GET', '/v1/health' );
			$health                = 200 === $status ? json_decode( $body, true ) : null;
			$mode                  = is_array( $health ) && isset( $health['mode'] ) && is_string( $health['mode'] ) ? $health['mode'] : 'down';
			set_transient( self::MODE, $mode, MINUTE_IN_SECONDS );
		}
		if ( 'seal' === $mode || 'observe' === $mode ) {
			return 'seal' === $mode;
		}
		return Rhythm_Echo::sealed();
	}

	/**
	 * Colours the banner: green when sealed, amber when observing.
	 */
	public static function banner_style(): void {
		if ( ! Rhythm_Echo::active() || ! is_admin_bar_showing() ) {
			return;
		}
		wp_add_inline_style(
			'admin-bar',
			'#wpadminbar .echo-staging-seal-sealed > .ab-item{background:#1e6b3c;color:#fff}' .
			'#wpadminbar .echo-staging-seal-observe > .ab-item{background:#b45309;color:#fff}'
		);
	}

	/**
	 * On production the connector does nothing. Say so, with how to turn
	 * it on for a copy that is not production.
	 */
	public static function production_notice(): void {
		if ( Rhythm_Echo::active() || ! current_user_can( 'manage_options' ) ) {
			return;
		}
		printf(
			'<div class="notice notice-warning"><p><strong>%s</strong> %s</p></div>',
			esc_html__( 'Echo Staging Seal is active but not sealing:', 'echo-staging-seal' ),
			esc_html__( 'WordPress says this is production, so Echo stays out of the way. On a staging copy set WP_ENVIRONMENT_TYPE to staging, development or local in wp-config.php.', 'echo-staging-seal' )
		);
	}

	// ---------------------------------------------------------------------
	// Tools > Echo.

	/**
	 * Adds Tools > Echo for administrators.
	 */
	public static function menu(): void {
		add_management_page(
			__( 'Echo Staging Seal', 'echo-staging-seal' ),
			__( 'Echo', 'echo-staging-seal' ),
			'manage_options',
			self::SLUG,
			array( __CLASS__, 'page' )
		);
	}

	/**
	 * Counts what Echo stopped, by plugin and channel: faked (Echo
	 * answered instead) and blocked (refused). Messages that went out
	 * (observe mode) or failed are left out. So are other sites' when
	 * this one has a site ID and Echo serves many.
	 *
	 * @param array<mixed> $messages Echo's /v1/messages.
	 * @param string       $site     this site's ID, or "".
	 * @return list<array{plugin: string, channel: string, faked: int, blocked: int}>
	 */
	public static function stopped( array $messages, string $site = '' ): array {
		$rows = array();
		foreach ( $messages as $m ) {
			if ( ! is_array( $m ) ) {
				continue;
			}
			$from = isset( $m['source'] ) && is_array( $m['source'] ) && isset( $m['source']['site'] ) && is_string( $m['source']['site'] ) ? $m['source']['site'] : '';
			if ( '' !== $site && '' !== $from && $from !== $site ) {
				continue;
			}
			$status = isset( $m['status'] ) && is_string( $m['status'] ) ? $m['status'] : '';
			if ( 'faked' !== $status && 'blocked' !== $status ) {
				continue;
			}
			$source  = isset( $m['source'] ) && is_array( $m['source'] ) ? $m['source'] : array();
			$plugin  = isset( $source['plugin'] ) && is_string( $source['plugin'] ) ? $source['plugin'] : '';
			$channel = isset( $m['channel'] ) && is_string( $m['channel'] ) ? $m['channel'] : 'http';
			$key     = $plugin . "\0" . $channel;
			if ( ! isset( $rows[ $key ] ) ) {
				$rows[ $key ] = array(
					'plugin'  => $plugin,
					'channel' => $channel,
					'faked'   => 0,
					'blocked' => 0,
				);
			}
			++$rows[ $key ][ $status ];
		}
		$rows = array_values( $rows );
		usort(
			$rows,
			static function ( array $a, array $b ): int {
				$by = ( $b['faked'] + $b['blocked'] ) <=> ( $a['faked'] + $a['blocked'] );
				return 0 !== $by ? $by : strcmp( $a['plugin'] . $a['channel'], $b['plugin'] . $b['channel'] );
			}
		);
		return $rows;
	}

	/**
	 * The stopped sends from Echo, or why they could not be read.
	 *
	 * @return array{rows: list<array{plugin: string, channel: string, faked: int, blocked: int}>, error: string}
	 */
	private static function log(): array {
		$cfg                         = Rhythm_Echo::config();
		list( $status, $body, $err ) = Rhythm_Echo::call( 'GET', '/v1/messages' );
		if ( 200 !== $status ) {
			$why = '' !== $err ? $err : 'HTTP ' . $status;
			// A hub site holds a write token, which Echo lets send but not read.
			$hub = $cfg['send_token'] !== $cfg['token'] || '' !== $cfg['site_id'];
			if ( 403 === $status && $hub ) {
				return array(
					'rows'  => array(),
					/* translators: %s: Echo's answer, like "HTTP 403" */
					'error' => sprintf( __( 'Echo refused to list messages (%s): this site\'s token can only send captures. See what was stopped in the hub\'s inbox.', 'echo-staging-seal' ), $why ),
				);
			}
			if ( 401 === $status || 403 === $status ) {
				return array(
					'rows'  => array(),
					/* translators: %s: Echo's answer, like "HTTP 403" */
					'error' => sprintf( __( 'Echo refused to list messages (%s): set the token Echo was started with.', 'echo-staging-seal' ), $why ),
				);
			}
			return array(
				'rows'  => array(),
				/* translators: %s: the error, like "Connection refused" */
				'error' => sprintf( __( 'Echo could not be reached (%s).', 'echo-staging-seal' ), $why ),
			);
		}
		$messages = json_decode( $body, true );
		return array(
			'rows'  => self::stopped( is_array( $messages ) ? $messages : array(), $cfg['site_id'] ),
			'error' => '',
		);
	}

	/**
	 * Renders Tools > Echo.
	 */
	public static function page(): void {
		if ( ! current_user_can( 'manage_options' ) ) {
			wp_die( esc_html__( 'Sorry, you are not allowed to see Echo.', 'echo-staging-seal' ), '', array( 'response' => 403 ) );
		}
		echo '<div class="wrap"><h1>' . esc_html__( 'Echo Staging Seal', 'echo-staging-seal' ) . '</h1>';
		self::page_notices();
		self::page_status();
		self::page_log();
		self::page_updates();
		self::page_settings();
		echo '</div>';
	}

	private static function page_notices(): void {
		// phpcs:ignore WordPress.Security.NonceVerification.Recommended, WordPress.Security.ValidatedSanitizedInput.InputNotSanitized -- only picks which notice to show
		$done = isset( $_GET['echo'] ) && is_string( $_GET['echo'] ) ? sanitize_key( wp_unslash( $_GET['echo'] ) ) : '';
		if ( 'saved' === $done ) {
			echo '<div class="notice notice-success"><p>' . esc_html__( 'Settings saved.', 'echo-staging-seal' ) . '</p></div>';
		}
		if ( 'bad-token' === $done ) {
			echo '<div class="notice notice-error"><p>' . esc_html__( 'Settings not saved: the token can only hold letters, digits and punctuation, with no spaces (up to 512 characters). Paste the token Echo was started with.', 'echo-staging-seal' ) . '</p></div>';
		}
		if ( self::$mu_plugin ) {
			echo '<div class="notice notice-info"><p>' . esc_html__( 'The Rhythm Echo mu-plugin is installed and runs the seal, with its own settings file. The settings below do not reach it.', 'echo-staging-seal' ) . '</p></div>';
		}
	}

	private static function page_status(): void {
		$cfg = Rhythm_Echo::config();
		if ( ! Rhythm_Echo::active() ) {
			$state = __( 'Not sealing: WordPress says this is production.', 'echo-staging-seal' );
		} elseif ( self::sealed() ) {
			$state = __( 'Sealed: nothing leaves this site. Echo answers for every service.', 'echo-staging-seal' );
		} else {
			$state = __( 'Observing: every send is recorded and goes out for real.', 'echo-staging-seal' );
		}
		echo '<p><strong>' . esc_html( $state ) . '</strong></p>';
		/* translators: %s: Echo's URL */
		echo '<p>' . esc_html( sprintf( __( 'Echo: %s', 'echo-staging-seal' ), $cfg['url'] ) ) . '</p>';
	}

	private static function page_log(): void {
		echo '<h2>' . esc_html__( 'Sends stopped', 'echo-staging-seal' ) . '</h2>';
		$log = self::log();
		if ( '' !== $log['error'] ) {
			echo '<div class="notice notice-error inline"><p>' . esc_html( $log['error'] ) . '</p></div>';
			return;
		}
		if ( array() === $log['rows'] ) {
			echo '<p>' . esc_html__( 'Echo has not stopped anything from this site yet.', 'echo-staging-seal' ) . '</p>';
			return;
		}
		echo '<table class="widefat striped" id="echo-staging-seal-log"><thead><tr><th>' . esc_html__( 'Plugin', 'echo-staging-seal' ) . '</th><th>' . esc_html__( 'Channel', 'echo-staging-seal' ) . '</th><th>' . esc_html__( 'Faked', 'echo-staging-seal' ) . '</th><th>' . esc_html__( 'Blocked', 'echo-staging-seal' ) . '</th></tr></thead><tbody>';
		foreach ( $log['rows'] as $row ) {
			printf(
				'<tr><td>%s</td><td>%s</td><td>%d</td><td>%d</td></tr>',
				esc_html( '' !== $row['plugin'] ? $row['plugin'] : __( '(unknown)', 'echo-staging-seal' ) ),
				esc_html( $row['channel'] ),
				(int) $row['faked'],
				(int) $row['blocked']
			);
		}
		echo '</tbody></table>';
		echo '<p class="description">' . esc_html__( 'Faked: Echo answered instead of the real service. Blocked: Echo refused the request.', 'echo-staging-seal' ) . '</p>';
	}

	private static function page_updates(): void {
		echo '<h2>' . esc_html__( 'Updates', 'echo-staging-seal' ) . '</h2>';
		echo '<p>' . esc_html__( 'Snapshot what every action sends before you update plugins, then compare after.', 'echo-staging-seal' ) . '</p>';
		$why = self::unavailable();
		if ( '' === $why ) {
			foreach ( array(
				'snapshot' => __( 'Snapshot before updating', 'echo-staging-seal' ),
				'compare'  => __( 'Compare since last update', 'echo-staging-seal' ),
			) as $command => $label ) {
				echo '<form method="post" action="' . esc_url( admin_url( 'admin-post.php' ) ) . '" style="display:inline-block;margin-right:8px">';
				echo '<input type="hidden" name="action" value="echo_staging_seal_run">';
				echo '<input type="hidden" name="command" value="' . esc_attr( $command ) . '">';
				wp_nonce_field( 'echo_staging_seal_run' );
				submit_button( $label, 'secondary', 'submit', false );
				echo '</form>';
			}
		} else {
			echo '<p>' . esc_html( $why ) . ' ' . esc_html__( 'Run these where Echo is installed instead:', 'echo-staging-seal' ) . '</p>';
			echo '<pre><code>rhythm echo snapshot --name ' . esc_html( self::BEFORE_UPDATES ) . "\nrhythm echo compare --snapshot " . esc_html( self::BEFORE_UPDATES ) . '</code></pre>';
		}
		$out = get_transient( self::OUTPUT . get_current_user_id() );
		if ( is_array( $out ) && isset( $out['command'], $out['code'], $out['output'] ) && is_string( $out['command'] ) && is_int( $out['code'] ) && is_string( $out['output'] ) ) {
			/* translators: 1: the command, 2: its exit code */
			echo '<h3>' . esc_html( sprintf( __( '%1$s (exit code %2$d)', 'echo-staging-seal' ), $out['command'], $out['code'] ) ) . '</h3>';
			echo '<pre id="echo-staging-seal-output" style="max-height:30em;overflow:auto;background:#fff;padding:8px">' . esc_html( $out['output'] ) . '</pre>';
		}
	}

	private static function page_settings(): void {
		$saved = self::stored();
		$cfg   = Rhythm_Echo::config();
		echo '<h2>' . esc_html__( 'Settings', 'echo-staging-seal' ) . '</h2>';
		echo '<form method="post" action="' . esc_url( admin_url( 'admin-post.php' ) ) . '">';
		echo '<input type="hidden" name="action" value="echo_staging_seal_settings">';
		wp_nonce_field( 'echo_staging_seal_settings' );
		echo '<table class="form-table" role="presentation">';
		if ( self::url_locked( Rhythm_Echo::settings_file() ) ) {
			printf(
				'<tr><th scope="row"><label for="echo_url">%s</label></th><td><input type="url" class="regular-text" id="echo_url" value="%s" readonly disabled><p class="description">%s</p></td></tr>',
				esc_html__( 'Echo URL', 'echo-staging-seal' ),
				esc_attr( $cfg['url'] ),
				esc_html__( 'The token is set outside this screen (wp-config.php, the environment or the installer\'s settings file), so the URL it goes to is too: set RHYTHM_ECHO_URL there.', 'echo-staging-seal' )
			);
		} else {
			printf(
				'<tr><th scope="row"><label for="echo_url">%s</label></th><td><input type="url" class="regular-text" id="echo_url" name="echo_url" value="%s" placeholder="http://127.0.0.1:8025"><p class="description">%s</p></td></tr>',
				esc_html__( 'Echo URL', 'echo-staging-seal' ),
				esc_attr( $saved['url'] ?? '' ),
				esc_html__( 'Echo\'s API as this site reaches it.', 'echo-staging-seal' )
			);
		}
		printf(
			'<tr><th scope="row"><label for="echo_token">%s</label></th><td><input type="password" class="regular-text" id="echo_token" name="echo_token" value="" autocomplete="off" placeholder="%s"> <label><input type="checkbox" name="echo_token_clear" value="1"> %s</label><p class="description">%s</p></td></tr>',
			esc_html__( 'Token', 'echo-staging-seal' ),
			esc_attr( '' !== $cfg['token'] ? __( '(set: leave blank to keep it)', 'echo-staging-seal' ) : '' ),
			esc_html__( 'Clear', 'echo-staging-seal' ),
			esc_html__( 'The token Echo was started with (rhythm echo up --token). It is sent over plain HTTP: use it only where the path to Echo is trusted.', 'echo-staging-seal' )
		);
		printf(
			'<tr><th scope="row"><label for="echo_site_id">%s</label></th><td><input type="text" class="regular-text" id="echo_site_id" name="echo_site_id" value="%s" placeholder="client-a"><p class="description">%s</p></td></tr>',
			esc_html__( 'Site ID', 'echo-staging-seal' ),
			esc_attr( $saved['site_id'] ?? '' ),
			esc_html__( 'Only when one Echo serves many sites (an agency hub): names this site in everything it sends. Lowercase letters, digits, dashes and underscores.', 'echo-staging-seal' )
		);
		$mode = $saved['mode'] ?? 'seal';
		printf(
			'<tr><th scope="row"><label for="echo_mode">%s</label></th><td><select id="echo_mode" name="echo_mode"><option value="seal"%s>%s</option><option value="observe"%s>%s</option></select><p class="description">%s</p></td></tr>',
			esc_html__( 'When Echo is down', 'echo-staging-seal' ),
			selected( $mode, 'seal', false ),
			esc_html__( 'Seal: send nothing', 'echo-staging-seal' ),
			selected( $mode, 'observe', false ),
			esc_html__( 'Observe: send anyway', 'echo-staging-seal' ),
			esc_html__( 'While Echo answers, it decides. This only matters when it cannot be reached.', 'echo-staging-seal' )
		);
		$runner = self::runner();
		printf(
			'<tr><th scope="row">%s</th><td><code>%s</code><p class="description">%s</p></td></tr>',
			esc_html__( 'rhythm-echo', 'echo-staging-seal' ),
			esc_html( '' !== $runner['bin'] ? $runner['bin'] : __( 'not set', 'echo-staging-seal' ) ),
			esc_html__( 'Set RHYTHM_ECHO_BIN (the binary) and RHYTHM_ECHO_PROJECT (the folder with .echo/config.yaml) in wp-config.php to run snapshots and compares from here.', 'echo-staging-seal' )
		);
		echo '</table>';
		submit_button( __( 'Save settings', 'echo-staging-seal' ) );
		echo '</form>';
	}

	// ---------------------------------------------------------------------
	// Running rhythm-echo.

	/**
	 * Why rhythm-echo can't be run from here, or "" when it can.
	 */
	public static function unavailable(): string {
		$bin = self::runner()['bin'];
		if ( '' === $bin ) {
			return __( 'No rhythm-echo binary is set (RHYTHM_ECHO_BIN in wp-config.php).', 'echo-staging-seal' );
		}
		if ( ! function_exists( 'proc_open' ) ) {
			return __( 'This server does not allow proc_open, so the commands cannot run from here.', 'echo-staging-seal' );
		}
		if ( ! is_file( $bin ) || ! is_executable( $bin ) ) {
			/* translators: %s: the path */
			return sprintf( __( '%s is not an executable file.', 'echo-staging-seal' ), $bin );
		}
		return '';
	}

	/**
	 * The rhythm-echo argument list for a command: snapshot (with a name)
	 * or compare (with a snapshot to compare against, if any), or null for
	 * anything else. Names follow Echo's snapshot rules. One can never
	 * start with a dash, so it is never read as a flag.
	 *
	 * @return list<string>|null
	 */
	public static function argv( string $command, string $name = '' ): ?array {
		$bin = self::runner()['bin'];
		if ( '' !== $name && 1 !== preg_match( '/^[A-Za-z0-9_][A-Za-z0-9._-]{0,99}$/', $name ) ) {
			return null;
		}
		switch ( $command ) {
			case 'snapshot':
				return '' === $name ? null : array( $bin, 'snapshot', '--name', $name );
			case 'compare':
				return '' === $name ? array( $bin, 'compare' ) : array( $bin, 'compare', '--snapshot', $name );
			case 'snapshots':
				return array( $bin, 'snapshots' );
		}
		return null;
	}

	/**
	 * Runs rhythm-echo with proc_open: no shell, so nothing in the
	 * arguments is interpreted. With $stream (WP-CLI) its output goes
	 * straight to this process's stdout and stderr as it comes; otherwise
	 * it is collected. Returns the exit code and what was collected.
	 *
	 * @param list<string> $argv   the program and its arguments.
	 * @param bool         $stream pass output through instead of collecting it.
	 * @return array{0: int, 1: string}
	 */
	public static function run( array $argv, bool $stream = false ): array {
		$project = self::runner()['project'];
		$cwd     = '' !== $project && is_dir( $project ) ? $project : ABSPATH;
		$spec    = $stream && defined( 'STDOUT' ) && defined( 'STDERR' )
			? array( 0 => array( 'pipe', 'r' ), 1 => STDOUT, 2 => STDERR )
			: array( 0 => array( 'pipe', 'r' ), 1 => array( 'pipe', 'w' ), 2 => array( 'pipe', 'w' ) );
		$pipes = array();
		$proc  = proc_open( $argv, $spec, $pipes, $cwd ); // phpcs:ignore Generic.PHP.ForbiddenFunctions.Found -- the point of the feature: see unavailable()
		if ( ! is_resource( $proc ) ) {
			return array( 127, __( 'rhythm-echo could not be started.', 'echo-staging-seal' ) );
		}
		fclose( $pipes[0] ); // phpcs:ignore WordPress.WP.AlternativeFunctions.file_system_operations_fclose
		$collected = self::drain( array_slice( $pipes, 1 ) );
		return array( proc_close( $proc ), $collected );
	}

	/**
	 * Reads stdout and stderr together as they come, so neither fills its
	 * pipe and stalls the command. Keeps the first MAX_OUTPUT bytes.
	 *
	 * @param array<resource> $open the pipes to read.
	 */
	private static function drain( array $open ): string {
		$collected = '';
		while ( array() !== $open ) {
			$read   = $open;
			$write  = null;
			$except = null;
			if ( false === stream_select( $read, $write, $except, 5 ) ) {
				break;
			}
			foreach ( $read as $pipe ) {
				$chunk = fread( $pipe, 8192 ); // phpcs:ignore WordPress.WP.AlternativeFunctions.file_system_operations_fread
				if ( is_string( $chunk ) && '' !== $chunk ) {
					if ( strlen( $collected ) < self::MAX_OUTPUT ) {
						$collected .= $chunk;
					}
					continue;
				}
				if ( feof( $pipe ) ) {
					fclose( $pipe ); // phpcs:ignore WordPress.WP.AlternativeFunctions.file_system_operations_fclose
					$open = array_filter(
						$open,
						static function ( $p ) use ( $pipe ): bool {
							return $p !== $pipe;
						}
					);
				}
			}
		}
		return substr( $collected, 0, self::MAX_OUTPUT );
	}

	/**
	 * Runs a button's command and shows its output on Tools > Echo. Needs
	 * manage_options and the button's nonce.
	 */
	public static function run_command(): void {
		if ( ! current_user_can( 'manage_options' ) ) {
			wp_die( esc_html__( 'Sorry, you are not allowed to run Echo.', 'echo-staging-seal' ), '', array( 'response' => 403 ) );
		}
		check_admin_referer( 'echo_staging_seal_run' );
		// Both buttons use the same snapshot, so a snapshot taken in between
		// by hand or by CI does not change what "since last update" means.
		$command = sanitize_key( self::posted( 'command' ) );
		$argv    = self::argv( $command, in_array( $command, array( 'snapshot', 'compare' ), true ) ? self::BEFORE_UPDATES : '' );
		$why     = self::unavailable();
		if ( null === $argv || '' !== $why ) {
			wp_die( esc_html( '' !== $why ? $why : __( 'Unknown command.', 'echo-staging-seal' ) ), '', array( 'response' => 400 ) );
		}
		if ( function_exists( 'set_time_limit' ) ) {
			set_time_limit( 900 ); // A snapshot runs every action in the project.
		}
		list( $code, $output ) = self::run( $argv );
		set_transient(
			self::OUTPUT . get_current_user_id(),
			array(
				'command' => 'rhythm echo ' . implode( ' ', array_slice( $argv, 1 ) ),
				'code'    => $code,
				'output'  => $output,
			),
			HOUR_IN_SECONDS
		);
		self::back( 'ran' );
	}

	private static function back( string $done ): void {
		wp_safe_redirect( admin_url( 'tools.php?page=' . self::SLUG . '&echo=' . $done ) );
		exit;
	}
}
