<?php
/**
 * Plugin Name: Rhythm Echo
 * Plugin URI:  https://github.com/RhythmShahriar/echo
 * Description: Reports every email and outbound HTTP request this site makes to Rhythm Echo. Sealed (the default), nothing leaves: Echo answers instead. Observe: everything is recorded and sent. Installed by `rhythm echo install wordpress`; delete this file to remove it.
 * Version:     1.0.0
 * Requires PHP: 7.4
 * Author:      Rhythm
 * License:     MIT
 *
 * rhythm-echo-connector (Echo looks for this marker before it removes the file)
 *
 * How it hooks in, and why:
 *
 * - Email: this file defines wp_mail(). mu-plugins load before plugins, so
 *   it wins over SMTP plugins that replace wp_mail() too (FluentSMTP, Post
 *   SMTP): a sealed site must not hand mail to a real mailer. Observed mail
 *   is sent the way core sends it, with core's filters and actions. If
 *   something defined wp_mail() first, Echo falls back to pre_wp_mail and
 *   phpmailer_init and says so in its warnings.
 * - HTTP: pre_http_request sees every wp_remote_*() call (API mailers, SMS
 *   plugins, WooCommerce webhooks, Slack, GA4, Meta CAPI) before it is sent;
 *   Echo answers allow, fake or block. Allowed requests are reported again
 *   from http_api_debug with their response. The site's own host is never
 *   reported (cron and Action Scheduler loopbacks).
 * - Runs: the X-Echo-Run header or cookie of the request that caused a send
 *   ties it to an action, then RHYTHM_ECHO_RUN in the environment (WP-CLI,
 *   cron and queue flushes Echo runs), then a run stored with the scheduled
 *   cron event or Action Scheduler action when it was queued. Anything else
 *   falls in Echo's open action window.
 * - Fail closed: if Echo can't be reached while sealed, nothing is sent and
 *   the reason goes to the PHP error log.
 * - Production: on a production site (wp_get_environment_type(), which is
 *   "production" when WP_ENVIRONMENT_TYPE isn't set) it does nothing but
 *   show an admin notice, unless RHYTHM_ECHO_FORCE is set.
 *
 * - Proxy (optional): with RHYTHM_ECHO_PROXY set, raw cURL in this
 *   request (SDKs that skip the HTTP API) goes through Echo's proxy, and
 *   RHYTHM_ECHO_CA (Echo's CA PEM) is added to the HTTP API's CA bundle.
 *
 * Settings, first found wins: define() constants RHYTHM_ECHO_URL,
 * RHYTHM_ECHO_TOKEN, RHYTHM_ECHO_MODE, RHYTHM_ECHO_FORCE, RHYTHM_ECHO_PROXY,
 * RHYTHM_ECHO_CA; the same names in
 * the environment; mu-plugins/rhythm-echo/settings.php (written by the
 * installer, returns an array).
 *
 * PHP 7.4 or later, WordPress 5.7 or later.
 */

if ( ! defined( 'ABSPATH' ) ) {
	return;
}

/**
 * The connector. Static because WordPress hooks are global and there is
 * only ever one of it per request.
 */
final class Rhythm_Echo {

	const VERSION = '1.0.0';
	const WIRE    = 1;

	/** Bytes of request or response body reported to Echo. */
	const MAX_BODY = 2097152;

	/** The canary address and URL `rhythm echo doctor` expects to see caught. */
	const CANARY_EMAIL = 'canary@echo.invalid';
	const CANARY_URL   = 'https://canary.echo.invalid/';

	/** @var array{url: string, token: string, mode: string, force: bool, proxy: string, ca: string}|null */
	private static $config = null;

	/** @var bool */
	private static $active = false;

	/** @var string why it is inactive */
	private static $reason = '';

	/** @var string wp_mail (this file's own) or pre_wp_mail (fallback) */
	private static $layer = '';

	/** @var list<string> */
	private static $warnings = array();

	/** @var bool set while this connector talks to Echo through the WordPress HTTP API */
	private static $sending = false;

	/** @var string|null the run stored with the cron event or queued action running now */
	private static $context_run = null;

	/** @var array<string, string> request key → capture ID, for the after phase */
	private static $pending = array();

	/** @var int a counter making request keys unique */
	private static $seq = 0;

	/** @var bool fallback layer: pre_wp_mail already reported the mail now being sent */
	private static $mail_reported = false;

	/**
	 * Reads the settings and hooks in, unless this is production.
	 */
	public static function boot(): void {
		$cfg = self::config();
		add_action( 'rest_api_init', array( __CLASS__, 'routes' ) );
		if ( 'production' === self::environment() && ! $cfg['force'] ) {
			self::$reason = 'production';
			add_action( 'admin_notices', array( __CLASS__, 'production_notice' ) );
			return;
		}
		self::$active = true;

		add_filter( 'http_request_args', array( __CLASS__, 'http_request_args' ), PHP_INT_MAX, 2 );
		if ( '' !== $cfg['proxy'] ) {
			add_action( 'muplugins_loaded', array( __CLASS__, 'use_proxy' ), 0 );
		}
		// Last, so a plugin that already answers a request (a mock, a cache) wins.
		add_filter( 'pre_http_request', array( __CLASS__, 'pre_http_request' ), PHP_INT_MAX, 3 );
		add_action( 'http_api_debug', array( __CLASS__, 'http_api_debug' ), 10, 5 );
		add_action( 'http_api_curl', array( __CLASS__, 'http_api_curl' ), 10, 2 );

		// Runs for WP-Cron events and Action Scheduler actions.
		add_filter( 'schedule_event', array( __CLASS__, 'schedule_event' ), PHP_INT_MAX );
		add_filter( 'pre_reschedule_event', array( __CLASS__, 'pre_reschedule_event' ), 0, 2 );
		add_filter( 'pre_unschedule_event', array( __CLASS__, 'pre_unschedule_event' ), 0, 4 );
		add_action( 'action_scheduler_stored_action', array( __CLASS__, 'as_stored' ) );
		add_action( 'action_scheduler_before_execute', array( __CLASS__, 'as_before' ) );
		add_action( 'action_scheduler_after_execute', array( __CLASS__, 'as_after' ) );
		add_action( 'action_scheduler_failed_execution', array( __CLASS__, 'as_after' ) );
	}

	public static function active(): bool {
		return self::$active;
	}

	/**
	 * Records which email layer is in use. The fallback layer is a warning:
	 * another wp_mail() may not fire pre_wp_mail or phpmailer_init at all.
	 */
	public static function use_layer( string $layer ): void {
		self::$layer = $layer;
		if ( 'pre_wp_mail' !== $layer ) {
			return;
		}
		$where = '';
		try {
			$fn    = new ReflectionFunction( 'wp_mail' );
			$where = (string) $fn->getFileName();
		} catch ( ReflectionException $e ) {
			$where = 'unknown';
		}
		self::warn( sprintf( 'wp_mail() was already defined (%s) before Rhythm Echo loaded, so Echo catches mail through pre_wp_mail and phpmailer_init instead. Mail sent by that wp_mail() without those hooks is not caught.', self::relative( $where ) ) );
		add_filter( 'pre_wp_mail', array( __CLASS__, 'pre_wp_mail' ), PHP_INT_MAX, 2 );
		add_action( 'phpmailer_init', array( __CLASS__, 'phpmailer_init' ), PHP_INT_MAX );
	}

	private static function warn( string $message ): void {
		self::$warnings[] = $message;
	}

	// ---------------------------------------------------------------------
	// Settings.

	/**
	 * @return array{url: string, token: string, mode: string, force: bool, proxy: string, ca: string}
	 */
	public static function config(): array {
		if ( null !== self::$config ) {
			return self::$config;
		}
		$file = array();
		$path = __DIR__ . '/rhythm-echo/settings.php';
		if ( is_readable( $path ) ) {
			$loaded = include $path;
			if ( is_array( $loaded ) ) {
				$file = $loaded;
			}
		}
		$url  = self::setting( 'RHYTHM_ECHO_URL', $file, 'url' );
		$mode = strtolower( self::setting( 'RHYTHM_ECHO_MODE', $file, 'mode' ) );
		self::$config = array(
			'url'   => '' !== $url ? rtrim( $url, '/' ) : 'http://127.0.0.1:8025',
			'token' => self::setting( 'RHYTHM_ECHO_TOKEN', $file, 'token' ),
			'mode'  => 'observe' === $mode ? 'observe' : 'seal',
			'force' => self::truthy( self::setting( 'RHYTHM_ECHO_FORCE', $file, 'force' ) ),
			'proxy' => self::setting( 'RHYTHM_ECHO_PROXY', $file, 'proxy' ),
			'ca'    => self::setting( 'RHYTHM_ECHO_CA', $file, 'ca' ),
		);
		return self::$config;
	}

	/**
	 * A setting from a constant, the environment or the settings file.
	 *
	 * @param array<string, mixed> $file
	 */
	private static function setting( string $name, array $file, string $key ): string {
		if ( defined( $name ) ) {
			$v = constant( $name );
			return is_bool( $v ) ? ( $v ? '1' : '' ) : trim( (string) $v );
		}
		$env = getenv( $name );
		if ( false !== $env && '' !== $env ) {
			return trim( $env );
		}
		if ( isset( $file[ $key ] ) ) {
			$v = $file[ $key ];
			return is_bool( $v ) ? ( $v ? '1' : '' ) : trim( (string) $v );
		}
		return '';
	}

	private static function truthy( string $v ): bool {
		return in_array( strtolower( $v ), array( '1', 'true', 'yes', 'on' ), true );
	}

	private static function environment(): string {
		return function_exists( 'wp_get_environment_type' ) ? wp_get_environment_type() : 'production';
	}

	private static function sealed(): bool {
		return 'observe' !== self::config()['mode'];
	}

	public static function production_notice(): void {
		if ( ! current_user_can( 'manage_options' ) ) {
			return;
		}
		$why = ( false === getenv( 'WP_ENVIRONMENT_TYPE' ) && ! defined( 'WP_ENVIRONMENT_TYPE' ) )
			? 'WP_ENVIRONMENT_TYPE is not set, and WordPress treats that as production.'
			: 'WP_ENVIRONMENT_TYPE is production.';
		printf(
			'<div class="notice notice-warning"><p><strong>Rhythm Echo is installed but not running:</strong> %s Echo only runs on test sites. Set WP_ENVIRONMENT_TYPE to staging, development or local on a test copy, or remove <code>wp-content/mu-plugins/rhythm-echo.php</code> (run <code>rhythm echo install wordpress --uninstall</code>).</p></div>',
			esc_html( $why )
		);
	}

	// ---------------------------------------------------------------------
	// Echo's proxy, for SDKs that skip the WordPress HTTP API.

	/**
	 * Points raw cURL (the Twilio and Stripe PHP SDKs, WP Mail SMTP's Gmail
	 * mailer) at Echo's proxy for this request. libcurl reads these when a
	 * handle has no CURLOPT_PROXY; PHP restores the environment when the
	 * request ends. The site itself, Echo and loopback stay direct, and so
	 * does this connector's own traffic (CURLOPT_PROXY '').
	 */
	public static function use_proxy(): void {
		$proxy = self::config()['proxy'];
		if ( '' === $proxy || ! function_exists( 'putenv' ) ) {
			return;
		}
		$direct = array( 'localhost', '127.0.0.1', '::1' );
		foreach ( array( home_url(), site_url(), self::config()['url'] ) as $u ) {
			$h = wp_parse_url( $u, PHP_URL_HOST );
			if ( is_string( $h ) && '' !== $h ) {
				$direct[] = strtolower( $h );
			}
		}
		$no_proxy = implode( ',', array_unique( $direct ) );
		foreach ( array( 'https_proxy', 'http_proxy', 'HTTPS_PROXY', 'HTTP_PROXY' ) as $name ) {
			putenv( $name . '=' . $proxy );
		}
		putenv( 'no_proxy=' . $no_proxy );
		putenv( 'NO_PROXY=' . $no_proxy );
	}

	/**
	 * Requests the connector already captured and let through go direct,
	 * not through Echo's proxy: otherwise they'd be captured twice, and
	 * hosts only the site can resolve would fail at the proxy.
	 *
	 * @param resource|\CurlHandle $handle
	 * @param array<string, mixed>  $args
	 */
	public static function http_api_curl( $handle, $args ): void {
		if ( '' === self::config()['proxy'] || ! is_array( $args ) || ! isset( $args['_rhythm_echo'] ) ) {
			return;
		}
		// phpcs:disable WordPress.WP.AlternativeFunctions.curl_curl_setopt
		curl_setopt( $handle, CURLOPT_PROXY, '' );
		if ( defined( 'CURLOPT_NOPROXY' ) ) {
			curl_setopt( $handle, CURLOPT_NOPROXY, '*' );
		}
		// phpcs:enable
	}

	/**
	 * The CA bundle for the WordPress HTTP API with Echo's CA added, so
	 * requests that reach Echo's TLS-intercepting proxy verify. Core's
	 * bundle (or whatever bundle the request already uses) is kept: the
	 * combined file is written once to the temp directory, named by its
	 * inputs, and reused.
	 */
	private static function ca_bundle( string $base ): string {
		$ca = self::config()['ca'];
		if ( '' === $ca || ! is_readable( $ca ) || ! is_readable( $base ) ) {
			return $base;
		}
		$path = get_temp_dir() . 'rhythm-echo-ca-' . md5( $base . '|' . filemtime( $base ) . '|' . md5_file( $ca ) ) . '.pem';
		if ( is_readable( $path ) ) {
			return $path;
		}
		$pem = (string) file_get_contents( $base ) . "\n# Rhythm Echo CA (" . $ca . ")\n" . (string) file_get_contents( $ca ) . "\n"; // phpcs:ignore WordPress.WP.AlternativeFunctions
		$tmp = $path . '.' . wp_generate_password( 8, false );
		if ( false === file_put_contents( $tmp, $pem ) || ! rename( $tmp, $path ) ) { // phpcs:ignore WordPress.WP.AlternativeFunctions
			@unlink( $tmp ); // phpcs:ignore WordPress.PHP.NoSilencedErrors
			error_log( 'Rhythm Echo: could not write the combined CA bundle to ' . $path ); // phpcs:ignore WordPress.PHP.DevelopmentFunctions
			return $base;
		}
		return $path;
	}

	// ---------------------------------------------------------------------
	// Runs.

	/**
	 * The "<run>/<action>" this send belongs to: the triggering request's
	 * header, its cookie, the environment, then the scheduled work's run.
	 */
	public static function run(): string {
		$candidates = array(
			isset( $_SERVER['HTTP_X_ECHO_RUN'] ) ? (string) $_SERVER['HTTP_X_ECHO_RUN'] : '', // phpcs:ignore WordPress.Security.ValidatedSanitizedInput
			isset( $_COOKIE['X-Echo-Run'] ) ? (string) $_COOKIE['X-Echo-Run'] : '', // phpcs:ignore WordPress.Security.ValidatedSanitizedInput
			// A queued job's own run beats the environment's: a flush for
			// action B must not claim jobs that action A queued.
			(string) self::$context_run,
			(string) getenv( 'RHYTHM_ECHO_RUN' ),
		);
		foreach ( $candidates as $v ) {
			$v = self::clean_run( $v );
			if ( '' !== $v ) {
				return $v;
			}
		}
		return '';
	}

	private static function clean_run( string $v ): string {
		$v = trim( (string) preg_replace( '/[\x00-\x1f\x7f]/', '', $v ) );
		return strlen( $v ) > 512 ? '' : $v;
	}

	/**
	 * Stores the current run with a cron event as it is scheduled, keyed by
	 * hook, arguments and time: event arguments are left alone, because
	 * they are part of the event's identity (wp_next_scheduled).
	 *
	 * @param object|false $event
	 * @return object|false
	 */
	public static function schedule_event( $event ) {
		if ( ! is_object( $event ) || ! isset( $event->hook, $event->timestamp ) ) {
			return $event;
		}
		$run = self::run();
		if ( '' !== $run ) {
			$args = isset( $event->args ) && is_array( $event->args ) ? $event->args : array();
			$ttl  = min( DAY_IN_SECONDS, max( 0, (int) $event->timestamp - time() ) + HOUR_IN_SECONDS );
			set_transient( self::cron_key( (int) $event->timestamp, (string) $event->hook, $args ), $run, $ttl );
		}
		return $event;
	}

	/**
	 * Core reschedules a recurring event just before running it.
	 *
	 * @param mixed  $pre
	 * @param object $event
	 * @return mixed
	 */
	public static function pre_reschedule_event( $pre, $event ) {
		if ( self::running_scheduled() && is_object( $event ) && isset( $event->hook, $event->timestamp ) ) {
			$args = isset( $event->args ) && is_array( $event->args ) ? $event->args : array();
			self::cron_context( (int) $event->timestamp, (string) $event->hook, $args );
		}
		return $pre;
	}

	/**
	 * Core (wp-cron.php and `wp cron event run`) unschedules each event just
	 * before running it: the only hook that says which event is next.
	 *
	 * @param mixed        $pre
	 * @param int          $timestamp
	 * @param string       $hook
	 * @param array<mixed> $args
	 * @return mixed
	 */
	public static function pre_unschedule_event( $pre, $timestamp, $hook, $args ) {
		if ( self::running_scheduled() ) {
			self::cron_context( (int) $timestamp, (string) $hook, is_array( $args ) ? $args : array() );
		}
		return $pre;
	}

	private static function running_scheduled(): bool {
		return ( function_exists( 'wp_doing_cron' ) && wp_doing_cron() ) || ( defined( 'WP_CLI' ) && WP_CLI );
	}

	/**
	 * @param array<mixed> $args
	 */
	private static function cron_context( int $timestamp, string $hook, array $args ): void {
		$key = self::cron_key( $timestamp, $hook, $args );
		$run = get_transient( $key );
		if ( is_string( $run ) && '' !== $run ) {
			self::$context_run = $run;
			delete_transient( $key );
		} elseif ( null !== self::$context_run ) {
			// A later event in the same cron request carries no run of its own.
			self::$context_run = null;
		}
	}

	/**
	 * @param array<mixed> $args
	 */
	private static function cron_key( int $timestamp, string $hook, array $args ): string {
		return 'rhythm_echo_cron_' . md5( $timestamp . '|' . $hook . '|' . serialize( $args ) ); // phpcs:ignore WordPress.PHP.DiscouragedPHPFunctions
	}

	/**
	 * Action Scheduler (WooCommerce async webhooks and emails): remember the
	 * run with the action's ID when it is stored…
	 *
	 * @param int|string $action_id
	 */
	public static function as_stored( $action_id ): void {
		$run = self::run();
		if ( '' !== $run ) {
			set_transient( 'rhythm_echo_as_' . $action_id, $run, DAY_IN_SECONDS );
		}
	}

	/**
	 * …and use it while the action runs.
	 *
	 * @param int|string $action_id
	 */
	public static function as_before( $action_id ): void {
		$run               = get_transient( 'rhythm_echo_as_' . $action_id );
		self::$context_run = is_string( $run ) && '' !== $run ? $run : null;
	}

	/**
	 * @param int|string $action_id
	 */
	public static function as_after( $action_id ): void {
		delete_transient( 'rhythm_echo_as_' . $action_id );
		self::$context_run = null;
	}

	// ---------------------------------------------------------------------
	// HTTP.

	/**
	 * Tags each request so its after phase can find its capture, and passes
	 * the run on to the site's own loopback requests (async runners).
	 *
	 * @param array<string, mixed> $args
	 * @param string               $url
	 * @return array<string, mixed>
	 */
	public static function http_request_args( $args, $url ) {
		if ( ! is_array( $args ) ) {
			return $args;
		}
		$args['_rhythm_echo'] = 'r' . ( ++self::$seq );
		if ( '' !== self::config()['ca'] ) {
			$base                    = isset( $args['sslcertificates'] ) && is_string( $args['sslcertificates'] ) ? $args['sslcertificates'] : ABSPATH . WPINC . '/certificates/ca-bundle.crt';
			$args['sslcertificates'] = self::ca_bundle( $base );
		}
		$run = self::run();
		if ( '' !== $run && self::own_host( (string) $url ) && is_array( $args['headers'] ?? null ) && ! isset( $args['headers']['X-Echo-Run'] ) ) {
			$args['headers']['X-Echo-Run'] = $run;
		}
		return $args;
	}

	/**
	 * Reports the request and obeys Echo: fake answers, block fails, allow
	 * lets core send it.
	 *
	 * @param false|array<string, mixed>|WP_Error $pre
	 * @param array<string, mixed>                $args
	 * @param string                              $url
	 * @return false|array<string, mixed>|WP_Error
	 */
	public static function pre_http_request( $pre, $args, $url ) {
		if ( false !== $pre || self::$sending || ! is_array( $args ) || self::skip_url( (string) $url ) ) {
			return $pre;
		}
		$capture = self::capture(
			'http',
			'pre_http_request',
			array(
				'phase' => 'before',
				'http'  => self::request( (string) $url, $args ),
			)
		);
		$d       = self::send( $capture );
		if ( null === $d ) {
			if ( self::sealed() ) {
				return new WP_Error( 'http_request_failed', 'Rhythm Echo could not be reached and this site is sealed, so the request was not sent.' );
			}
			return false;
		}
		$action = isset( $d['action'] ) ? (string) $d['action'] : '';
		if ( 'fake' === $action ) {
			$r = isset( $d['response'] ) && is_array( $d['response'] ) ? $d['response'] : array();
			return self::wp_response( $r, $args, (string) $url );
		}
		if ( 'allow' === $action ) {
			if ( isset( $d['id'], $args['_rhythm_echo'] ) ) {
				self::$pending[ (string) $args['_rhythm_echo'] ] = (string) $d['id'];
			}
			return false;
		}
		$host = (string) wp_parse_url( (string) $url, PHP_URL_HOST );
		return new WP_Error( 'http_request_failed', sprintf( 'Rhythm Echo blocked this request to %s: the site is sealed.', $host ) );
	}

	/**
	 * Reports what an allowed request got back.
	 *
	 * @param array<string, mixed>|WP_Error $response
	 * @param string                        $context
	 * @param string                        $class
	 * @param array<string, mixed>          $args
	 * @param string                        $url
	 */
	public static function http_api_debug( $response, $context, $class, $args, $url ): void {
		if ( 'response' !== $context || ! is_array( $args ) || ! isset( $args['_rhythm_echo'] ) ) {
			return;
		}
		$key = (string) $args['_rhythm_echo'];
		if ( ! isset( self::$pending[ $key ] ) ) {
			return;
		}
		$id = self::$pending[ $key ];
		unset( self::$pending[ $key ] );
		$http = array(
			'method' => strtoupper( isset( $args['method'] ) ? (string) $args['method'] : 'GET' ),
			'url'    => self::mask_url( (string) $url ),
		);
		$fields = array(
			'phase' => 'after',
			'id'    => $id,
		);
		if ( is_wp_error( $response ) ) {
			$fields['error'] = $response->get_error_message();
		} elseif ( is_array( $response ) ) {
			$http['status']   = (int) wp_remote_retrieve_response_code( $response );
			$http['response'] = self::cap( (string) wp_remote_retrieve_body( $response ), 65536 );
		}
		$fields['http'] = $http;
		self::send( self::capture( 'http', 'http_api_debug', $fields ) );
	}

	/**
	 * The site itself (cron and loopback calls), Echo, and non-HTTP URLs are
	 * never reported.
	 */
	private static function skip_url( string $url ): bool {
		$host = wp_parse_url( $url, PHP_URL_HOST );
		if ( ! is_string( $host ) || '' === $host ) {
			return true;
		}
		if ( self::own_host( $url ) ) {
			return true;
		}
		$echo = wp_parse_url( self::config()['url'] );
		return is_array( $echo ) && isset( $echo['host'] ) && strtolower( $echo['host'] ) === strtolower( $host )
			&& ( $echo['port'] ?? null ) === wp_parse_url( $url, PHP_URL_PORT );
	}

	private static function own_host( string $url ): bool {
		$host = wp_parse_url( $url, PHP_URL_HOST );
		if ( ! is_string( $host ) ) {
			return false;
		}
		$host = strtolower( $host );
		foreach ( array( home_url(), site_url() ) as $own ) {
			$h = wp_parse_url( $own, PHP_URL_HOST );
			if ( is_string( $h ) && strtolower( $h ) === $host ) {
				return true;
			}
		}
		return false;
	}

	/**
	 * The request as Echo's wire protocol has it, with credentials masked.
	 *
	 * @param array<string, mixed> $args
	 * @return array<string, mixed>
	 */
	private static function request( string $url, array $args ): array {
		$method  = strtoupper( isset( $args['method'] ) ? (string) $args['method'] : 'GET' );
		$headers = array();
		$raw     = $args['headers'] ?? array();
		if ( is_string( $raw ) ) {
			foreach ( preg_split( '/\r?\n/', $raw ) ?: array() as $line ) {
				if ( false !== strpos( $line, ':' ) ) {
					list( $k, $v ) = explode( ':', $line, 2 );
					$raw_k         = trim( $k );
					if ( '' !== $raw_k ) {
						$headers[ $raw_k ] = trim( $v );
					}
				}
			}
		} elseif ( is_array( $raw ) || $raw instanceof Traversable ) {
			foreach ( $raw as $k => $v ) {
				$headers[ (string) $k ] = is_array( $v ) ? implode( ', ', array_map( 'strval', $v ) ) : (string) $v;
			}
		}
		$body = $args['body'] ?? '';
		if ( is_array( $body ) || is_object( $body ) ) {
			$body = http_build_query( (array) $body, '', '&' );
		}
		$body = (string) $body;
		$type = '';
		foreach ( $headers as $k => $v ) {
			if ( 'content-type' === strtolower( $k ) ) {
				$type = strtolower( $v );
			}
			$headers[ $k ] = self::mask_header( $k, $v );
		}
		return array(
			'method'  => $method,
			'url'     => self::mask_url( $url ),
			'headers' => (object) $headers,
			'body'    => self::cap( self::mask_body( $body, $type ), self::MAX_BODY ),
		);
	}

	/**
	 * A WordPress HTTP response built from Echo's fake, shaped like core's
	 * own so wp_remote_retrieve_*() and SDK wrappers work unchanged.
	 *
	 * @param array<string, mixed> $r
	 * @param array<string, mixed> $args
	 * @return array<string, mixed>
	 */
	private static function wp_response( array $r, array $args, string $url ): array {
		$status  = isset( $r['status'] ) ? (int) $r['status'] : 200;
		$body    = isset( $r['body'] ) ? (string) $r['body'] : '';
		$headers = array();
		if ( isset( $r['headers'] ) && is_array( $r['headers'] ) ) {
			foreach ( $r['headers'] as $k => $v ) {
				$headers[ strtolower( (string) $k ) ] = (string) $v;
			}
		}
		$filename = null;
		if ( ! empty( $args['stream'] ) && ! empty( $args['filename'] ) ) {
			$filename = (string) $args['filename'];
			file_put_contents( $filename, $body ); // phpcs:ignore WordPress.WP.AlternativeFunctions
		}
		if ( class_exists( 'WpOrg\Requests\Utility\CaseInsensitiveDictionary' ) ) {
			$dict = new WpOrg\Requests\Utility\CaseInsensitiveDictionary( $headers );
		} elseif ( class_exists( 'Requests_Utility_CaseInsensitiveDictionary' ) ) {
			$dict = new Requests_Utility_CaseInsensitiveDictionary( $headers );
		} else {
			$dict = $headers;
		}
		$out = array(
			'headers'       => $dict,
			'body'          => $body,
			'response'      => array(
				'code'    => $status,
				'message' => get_status_header_desc( $status ),
			),
			'cookies'       => array(),
			'filename'      => $filename,
			'http_response' => null,
		);
		if ( class_exists( 'WP_HTTP_Requests_Response' ) && class_exists( 'WpOrg\Requests\Response' ) ) {
			$rr              = new WpOrg\Requests\Response();
			$rr->status_code = $status;
			$rr->body        = $body;
			$rr->url         = $url;
			$rr->success     = $status >= 200 && $status < 300;
			foreach ( $headers as $k => $v ) {
				$rr->headers[ $k ] = $v;
			}
			$out['http_response'] = new WP_HTTP_Requests_Response( $rr, (string) $filename );
		}
		return $out;
	}

	// ---------------------------------------------------------------------
	// Masking: captures never carry raw credentials. Like Echo's own
	// masking, a few leading characters stay so the kind of key shows
	// (sk_live_ vs sk_test_ decides whether a payment call may be faked).

	public static function mask( string $v, int $keep = 8 ): string {
		$len  = function_exists( 'mb_strlen' ) ? mb_strlen( $v ) : strlen( $v );
		$keep = min( $keep, intdiv( $len, 3 ) );
		$head = function_exists( 'mb_substr' ) ? mb_substr( $v, 0, $keep ) : substr( $v, 0, $keep );
		return $head . "\u{2022}\u{2022}\u{2022}\u{2022}\u{2022}";
	}

	private static function secret_name( string $name ): bool {
		return 1 === preg_match( '/(passw(or)?d|secret|token|api[_-]?key|apikey|auth[_-]?key|access[_-]?key|^key$|^authorization$|^cookie$)/i', $name );
	}

	private static function mask_header( string $name, string $value ): string {
		$lower = strtolower( $name );
		if ( 'x-echo-run' === $lower || 'content-type' === $lower ) {
			return $value;
		}
		if ( 'authorization' === $lower || 'proxy-authorization' === $lower ) {
			if ( preg_match( '/^(basic)\s+(\S+)$/i', $value, $m ) ) {
				$plain = base64_decode( $m[2], true ); // phpcs:ignore WordPress.PHP.DiscouragedPHPFunctions
				if ( false !== $plain && false !== strpos( $plain, ':' ) ) {
					list( $user, $pass ) = explode( ':', $plain, 2 );
					return $m[1] . ' ' . base64_encode( self::mask( $user ) . ':' . ( '' === $pass ? '' : self::mask( $pass, 0 ) ) ); // phpcs:ignore WordPress.PHP.DiscouragedPHPFunctions
				}
				return $m[1] . ' ' . self::mask( $m[2], 0 );
			}
			if ( preg_match( '/^([A-Za-z][A-Za-z0-9-]*)\s+(\S+)$/', $value, $m ) ) {
				return $m[1] . ' ' . self::mask( $m[2] );
			}
			return self::mask( $value );
		}
		if ( self::secret_name( $lower ) ) {
			return self::mask( $value );
		}
		return $value;
	}

	private static function mask_url( string $url ): string {
		$q = strpos( $url, '?' );
		if ( false === $q ) {
			return $url;
		}
		$frag = '';
		$hash = strpos( $url, '#', $q );
		if ( false !== $hash ) {
			$frag = substr( $url, $hash );
			$url  = substr( $url, 0, $hash );
		}
		return substr( $url, 0, $q + 1 ) . self::mask_form( substr( $url, $q + 1 ) ) . $frag;
	}

	private static function mask_form( string $form ): string {
		$out = preg_replace_callback(
			'/(^|&)([^=&]*)=([^&]*)/',
			static function ( array $m ): string {
				if ( ! self::secret_name( urldecode( $m[2] ) ) ) {
					return $m[0];
				}
				return $m[1] . $m[2] . '=' . rawurlencode( self::mask( urldecode( $m[3] ) ) );
			},
			$form
		);
		return is_string( $out ) ? $out : $form;
	}

	private static function mask_body( string $body, string $type ): string {
		if ( '' === $body ) {
			return $body;
		}
		$trim = ltrim( $body );
		if ( false !== strpos( $type, 'json' ) || ( '' !== $trim && ( '{' === $trim[0] || '[' === $trim[0] ) ) ) {
			$data = json_decode( $body, true );
			if ( is_array( $data ) ) {
				$changed = false;
				$data    = self::mask_json( $data, $changed );
				if ( ! $changed ) {
					return $body;
				}
				$enc = wp_json_encode( $data, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE );
				return is_string( $enc ) ? $enc : $body;
			}
			return $body;
		}
		if ( false !== strpos( $type, 'x-www-form-urlencoded' ) || ( '' === $type && 1 === preg_match( '/^[^\s=&]+=[^\s]*$/', $body ) ) ) {
			return self::mask_form( $body );
		}
		return $body;
	}

	/**
	 * @param array<mixed> $data
	 * @return array<mixed>
	 */
	private static function mask_json( array $data, bool &$changed ): array {
		foreach ( $data as $k => $v ) {
			if ( is_array( $v ) ) {
				$data[ $k ] = self::mask_json( $v, $changed );
			} elseif ( is_string( $k ) && is_scalar( $v ) && ! is_bool( $v ) && '' !== (string) $v && self::secret_name( $k ) ) {
				$data[ $k ] = self::mask( (string) $v );
				$changed    = true;
			}
		}
		return $data;
	}

	// ---------------------------------------------------------------------
	// Email.

	/**
	 * Replaces core's wp_mail(): the same filters and actions in the same
	 * order (wp_mail, pre_wp_mail, wp_mail_from, wp_mail_from_name,
	 * wp_mail_content_type, wp_mail_charset, wp_mail_embed_args,
	 * phpmailer_init, then wp_mail_succeeded or wp_mail_failed), so plugins
	 * see what they would see without Echo. The message is reported after
	 * phpmailer_init, as it would really go out; sealed, it stops there.
	 *
	 * @param string|string[] $to
	 * @param string          $subject
	 * @param string          $message
	 * @param string|string[] $headers
	 * @param string|string[] $attachments
	 * @param string|string[] $embeds
	 */
	public static function wp_mail( $to, $subject, $message, $headers = '', $attachments = array(), $embeds = array() ): bool {
		$atts = apply_filters( 'wp_mail', compact( 'to', 'subject', 'message', 'headers', 'attachments', 'embeds' ) );

		$pre = apply_filters( 'pre_wp_mail', null, $atts );
		if ( null !== $pre ) {
			// Another plugin answered for the mailer; report it, don't send.
			self::report_atts( is_array( $atts ) ? $atts : array(), false === $pre ? 'pre_wp_mail returned false: another plugin stopped this email' : '' );
			return (bool) $pre;
		}

		$to          = self::list_of( $atts['to'] ?? $to, ',' );
		$subject     = (string) ( $atts['subject'] ?? $subject );
		$message     = (string) ( $atts['message'] ?? $message );
		$headers     = $atts['headers'] ?? $headers;
		$attachments = self::list_of( $atts['attachments'] ?? $attachments, "\n" );
		$embeds      = self::list_of( $atts['embeds'] ?? $embeds, "\n" );

		$phpmailer = self::phpmailer();
		$parsed    = self::parse_headers( $headers );
		$headers   = $parsed['headers'];

		$phpmailer->clearAllRecipients();
		$phpmailer->clearAttachments();
		$phpmailer->clearCustomHeaders();
		$phpmailer->clearReplyTos();
		$phpmailer->Body     = '';
		$phpmailer->AltBody  = '';
		$phpmailer->Encoding = PHPMailer\PHPMailer\PHPMailer::ENCODING_8BIT;

		$from_email = apply_filters( 'wp_mail_from', null !== $parsed['from_email'] ? $parsed['from_email'] : self::default_from() );
		$from_name  = apply_filters( 'wp_mail_from_name', null !== $parsed['from_name'] ? $parsed['from_name'] : 'WordPress' );
		try {
			$phpmailer->setFrom( (string) $from_email, (string) $from_name, false );
		} catch ( PHPMailer\PHPMailer\Exception $e ) {
			$data                             = compact( 'to', 'subject', 'message', 'headers', 'attachments' );
			$data['phpmailer_exception_code'] = $e->getCode();
			do_action( 'wp_mail_failed', new WP_Error( 'wp_mail_failed', $e->getMessage(), $data ) );
			// Core fails here too (wordpress@localhost isn't a valid From):
			// report it, since the site would lose this email for real.
			$atts['headers'] = self::with_from( $atts['headers'] ?? '', (string) $from_email, (string) $from_name );
			self::report_atts( is_array( $atts ) ? $atts : array(), $e->getMessage() . ' (From: ' . $from_email . ')', 'wp_mail' );
			return false;
		}

		$phpmailer->Subject = $subject;
		$phpmailer->Body    = $message;

		$addresses = array(
			'to'       => $to,
			'cc'       => $parsed['cc'],
			'bcc'      => $parsed['bcc'],
			'reply_to' => $parsed['reply_to'],
		);
		foreach ( $addresses as $kind => $list ) {
			foreach ( $list as $address ) {
				$name = '';
				if ( preg_match( '/(.*)<(.+)>/', $address, $m ) && 3 === count( $m ) ) {
					$name    = $m[1];
					$address = $m[2];
				}
				try {
					switch ( $kind ) {
						case 'to':
							$phpmailer->addAddress( $address, $name );
							break;
						case 'cc':
							$phpmailer->addCC( $address, $name );
							break;
						case 'bcc':
							$phpmailer->addBCC( $address, $name );
							break;
						default:
							$phpmailer->addReplyTo( $address, $name );
					}
				} catch ( PHPMailer\PHPMailer\Exception $e ) {
					continue;
				}
			}
		}

		$phpmailer->isMail();

		$content_type           = apply_filters( 'wp_mail_content_type', null !== $parsed['content_type'] ? $parsed['content_type'] : 'text/plain' );
		$phpmailer->ContentType = (string) $content_type;
		if ( 'text/html' === $content_type ) {
			$phpmailer->isHTML( true );
		}
		$charset            = null !== $parsed['charset'] ? $parsed['charset'] : get_bloginfo( 'charset' );
		$phpmailer->CharSet = (string) apply_filters( 'wp_mail_charset', $charset );

		foreach ( $headers as $name => $content ) {
			if ( in_array( $name, array( 'MIME-Version', 'X-Mailer' ), true ) ) {
				continue;
			}
			try {
				$phpmailer->addCustomHeader( sprintf( '%1$s: %2$s', $name, $content ) );
			} catch ( PHPMailer\PHPMailer\Exception $e ) {
				continue;
			}
		}

		foreach ( $attachments as $filename => $attachment ) {
			try {
				$phpmailer->addAttachment( $attachment, is_string( $filename ) ? $filename : '' );
			} catch ( PHPMailer\PHPMailer\Exception $e ) {
				continue;
			}
		}

		foreach ( $embeds as $key => $path ) {
			$embed = apply_filters(
				'wp_mail_embed_args',
				array(
					'path'        => $path,
					'cid'         => (string) $key,
					'name'        => basename( $path ),
					'encoding'    => 'base64',
					'type'        => '',
					'disposition' => 'inline',
				)
			);
			try {
				$phpmailer->addEmbeddedImage( $embed['path'], $embed['cid'], $embed['name'], $embed['encoding'], $embed['type'], $embed['disposition'] );
			} catch ( PHPMailer\PHPMailer\Exception $e ) {
				continue;
			}
		}

		do_action_ref_array( 'phpmailer_init', array( &$phpmailer ) );

		$mail_data = compact( 'to', 'subject', 'message', 'headers', 'attachments', 'embeds' );
		$capture   = self::capture( 'email', 'wp_mail', array( 'email' => self::email_from_phpmailer( $phpmailer ) ) );
		$d         = self::send( $capture );
		if ( null === $d && self::sealed() ) {
			do_action( 'wp_mail_failed', new WP_Error( 'wp_mail_failed', 'Rhythm Echo could not be reached and this site is sealed, so the email was not sent.', $mail_data ) );
			return false;
		}
		if ( null !== $d && 'allow' !== ( $d['action'] ?? '' ) ) {
			// Sealed: Echo has it. The site carries on as if it was sent.
			do_action( 'wp_mail_succeeded', $mail_data );
			return true;
		}

		try {
			$sent = $phpmailer->send();
			do_action( 'wp_mail_succeeded', $mail_data );
			return $sent;
		} catch ( PHPMailer\PHPMailer\Exception $e ) {
			$mail_data['phpmailer_exception_code'] = $e->getCode();
			do_action( 'wp_mail_failed', new WP_Error( 'wp_mail_failed', $e->getMessage(), $mail_data ) );
			if ( null !== $d && isset( $d['id'] ) ) {
				// Tell Echo the observed send failed.
				self::send( self::capture( 'email', 'wp_mail_failed', array( 'phase' => 'after', 'id' => (string) $d['id'], 'error' => $e->getMessage() ) ) );
			}
			return false;
		}
	}

	/**
	 * Core's global PHPMailer, created the way core creates it.
	 *
	 * @return PHPMailer\PHPMailer\PHPMailer
	 */
	private static function phpmailer() {
		global $phpmailer;
		if ( ! ( $phpmailer instanceof PHPMailer\PHPMailer\PHPMailer ) ) {
			require_once ABSPATH . WPINC . '/PHPMailer/PHPMailer.php';
			require_once ABSPATH . WPINC . '/PHPMailer/SMTP.php';
			require_once ABSPATH . WPINC . '/PHPMailer/Exception.php';
			if ( file_exists( ABSPATH . WPINC . '/class-wp-phpmailer.php' ) ) {
				require_once ABSPATH . WPINC . '/class-wp-phpmailer.php';
			}
			$phpmailer = class_exists( 'WP_PHPMailer' ) ? new WP_PHPMailer( true ) : new PHPMailer\PHPMailer\PHPMailer( true );
			$phpmailer::$validator = static function ( $email ) {
				return (bool) is_email( $email );
			};
		}
		return $phpmailer;
	}

	private static function default_from(): string {
		$site = wp_parse_url( network_home_url(), PHP_URL_HOST );
		$from = 'wordpress@';
		if ( is_string( $site ) ) {
			$from .= 0 === strpos( $site, 'www.' ) ? substr( $site, 4 ) : $site;
		}
		return $from;
	}

	/**
	 * A string split on $sep, or the array as given (keys kept: string keys
	 * name attachments and embeds).
	 *
	 * @param mixed $v
	 * @return array<int|string, string>
	 */
	private static function list_of( $v, string $sep ): array {
		if ( is_array( $v ) ) {
			$out = array();
			foreach ( $v as $k => $item ) {
				if ( is_scalar( $item ) && '' !== trim( (string) $item ) ) {
					$out[ $k ] = (string) $item;
				}
			}
			return $out;
		}
		$v = (string) $v;
		if ( '' === trim( $v ) ) {
			return array();
		}
		$parts = explode( $sep, str_replace( "\r\n", "\n", $v ) );
		return array_values( array_filter( array_map( 'trim', $parts ), array( __CLASS__, 'filled' ) ) );
	}

	/**
	 * Parses wp_mail() headers the way core does.
	 *
	 * @param mixed $headers
	 * @return array{headers: array<string, string>, from_name: ?string, from_email: ?string, content_type: ?string, charset: ?string, cc: list<string>, bcc: list<string>, reply_to: list<string>}
	 */
	private static function parse_headers( $headers ): array {
		$out = array(
			'headers'      => array(),
			'from_name'    => null,
			'from_email'   => null,
			'content_type' => null,
			'charset'      => null,
			'cc'           => array(),
			'bcc'          => array(),
			'reply_to'     => array(),
		);
		if ( empty( $headers ) ) {
			return $out;
		}
		$lines    = is_array( $headers ) ? $headers : explode( "\n", str_replace( "\r\n", "\n", (string) $headers ) );
		$boundary = null;
		foreach ( $lines as $line ) {
			$line = (string) $line;
			if ( false === strpos( $line, ':' ) ) {
				if ( false !== stripos( $line, 'boundary=' ) ) {
					$parts    = preg_split( '/boundary=/i', trim( $line ) );
					$boundary = trim( str_replace( array( "'", '"' ), '', (string) ( $parts[1] ?? '' ) ) );
				}
				continue;
			}
			list( $name, $content ) = explode( ':', trim( $line ), 2 );
			$name                   = trim( $name );
			$content                = trim( $content );
			switch ( strtolower( $name ) ) {
				case 'from':
					$bracket = strpos( $content, '<' );
					if ( false !== $bracket ) {
						if ( $bracket > 0 ) {
							$out['from_name'] = trim( str_replace( '"', '', substr( $content, 0, $bracket ) ) );
						}
						$out['from_email'] = trim( str_replace( '>', '', substr( $content, $bracket + 1 ) ) );
					} elseif ( '' !== $content ) {
						$out['from_email'] = $content;
					}
					break;
				case 'content-type':
					if ( false !== strpos( $content, ';' ) ) {
						list( $type, $rest ) = explode( ';', $content, 2 );
						$out['content_type'] = trim( $type );
						if ( false !== stripos( $rest, 'charset=' ) ) {
							$out['charset'] = trim( str_ireplace( array( 'charset=', '"' ), '', $rest ) );
						} elseif ( false !== stripos( $rest, 'boundary=' ) ) {
							$boundary       = trim( str_ireplace( array( 'boundary=', '"' ), '', $rest ) );
							$out['charset'] = '';
						}
					} elseif ( '' !== $content ) {
						$out['content_type'] = $content;
					}
					break;
				case 'cc':
					$out['cc'] = array_merge( $out['cc'], array_map( 'trim', explode( ',', $content ) ) );
					break;
				case 'bcc':
					$out['bcc'] = array_merge( $out['bcc'], array_map( 'trim', explode( ',', $content ) ) );
					break;
				case 'reply-to':
					$out['reply_to'] = array_merge( $out['reply_to'], array_map( 'trim', explode( ',', $content ) ) );
					break;
				default:
					$out['headers'][ $name ] = $content;
			}
		}
		if ( null !== $boundary && null !== $out['content_type'] && preg_match( '~^multipart/(\S+)~', $out['content_type'], $m ) ) {
			$out['content_type'] = 'multipart/' . strtolower( $m[1] ) . '; boundary="' . $boundary . '"';
		}
		$out['cc']       = array_values( array_filter( $out['cc'], array( __CLASS__, 'filled' ) ) );
		$out['bcc']      = array_values( array_filter( $out['bcc'], array( __CLASS__, 'filled' ) ) );
		$out['reply_to'] = array_values( array_filter( $out['reply_to'], array( __CLASS__, 'filled' ) ) );
		return $out;
	}

	/**
	 * The email as it would go out: after every filter and phpmailer_init.
	 * Attachments are reported by name only.
	 *
	 * @param PHPMailer\PHPMailer\PHPMailer $mailer
	 * @return array<string, mixed>
	 */
	private static function email_from_phpmailer( $mailer ): array {
		$pick    = static function ( array $list ): array {
			return array_values(
				array_map(
					static function ( $a ) {
						return (string) $a[0];
					},
					$list
				)
			);
		};
		$headers = array();
		foreach ( $mailer->getCustomHeaders() as $h ) {
			$headers[ (string) $h[0] ] = (string) $h[1];
		}
		$headers['From']         = '' !== $mailer->FromName ? sprintf( '%s <%s>', $mailer->FromName, $mailer->From ) : $mailer->From;
		$headers['Content-Type'] = $mailer->ContentType . ( '' !== $mailer->CharSet && false === strpos( $mailer->ContentType, 'multipart/' ) ? '; charset=' . $mailer->CharSet : '' );
		$files                   = array();
		foreach ( $mailer->getAttachments() as $a ) {
			if ( 'inline' !== ( $a[6] ?? '' ) ) {
				$files[] = (string) ( '' !== (string) $a[2] ? $a[2] : $a[1] );
			}
		}
		if ( $files ) {
			$headers['X-Echo-Attachments'] = implode( ', ', $files );
		}
		$reply = $pick( array_values( $mailer->getReplyToAddresses() ) );
		$email = array(
			'from'     => $mailer->From,
			'to'       => $pick( $mailer->getToAddresses() ),
			'cc'       => $pick( $mailer->getCcAddresses() ),
			'bcc'      => $pick( $mailer->getBccAddresses() ),
			'reply_to' => $reply[0] ?? '',
			'subject'  => $mailer->Subject,
			'headers'  => (object) $headers,
		);
		$type = strtolower( $mailer->ContentType );
		if ( 'text/html' === $type ) {
			$email['html'] = self::cap( $mailer->Body, self::MAX_BODY );
			if ( '' !== $mailer->AltBody ) {
				$email['text'] = self::cap( $mailer->AltBody, self::MAX_BODY );
			}
		} elseif ( 0 === strpos( $type, 'multipart/' ) ) {
			// The body is already MIME parts: let Echo parse them.
			$email['raw'] = self::cap( 'Content-Type: ' . $mailer->ContentType . "\r\nSubject: " . $mailer->Subject . "\r\n\r\n" . $mailer->Body, self::MAX_BODY );
		} else {
			$email['text'] = self::cap( $mailer->Body, self::MAX_BODY );
		}
		return $email;
	}

	/**
	 * wp_mail() headers with From set to the filtered sender.
	 *
	 * @param mixed $headers
	 * @return list<string>
	 */
	private static function with_from( $headers, string $email, string $name ): array {
		$lines = is_array( $headers ) ? array_values( array_map( 'strval', $headers ) ) : explode( "\n", str_replace( "\r\n", "\n", (string) $headers ) );
		$lines = array_values(
			array_filter(
				$lines,
				static function ( string $l ): bool {
					return '' !== trim( $l ) && 0 !== stripos( ltrim( $l ), 'from:' );
				}
			)
		);
		$lines[] = 'From: ' . ( '' !== $name ? $name . ' <' . $email . '>' : $email );
		return $lines;
	}

	/**
	 * Reports mail that never reached PHPMailer: short-circuited through
	 * pre_wp_mail, or refused for a bad From.
	 *
	 * @param array<string, mixed> $atts
	 */
	private static function report_atts( array $atts, string $error, string $hook = 'pre_wp_mail' ): void {
		$fields = array( 'email' => self::email_from_atts( $atts ) );
		if ( '' !== $error ) {
			$fields['error'] = $error;
		}
		self::send( self::capture( 'email', $hook, $fields ) );
	}

	/**
	 * The email from wp_mail()'s arguments, for the fallback layer, where
	 * Echo sees the mail before any PHPMailer exists.
	 *
	 * @param array<string, mixed> $atts
	 * @return array<string, mixed>
	 */
	private static function email_from_atts( array $atts ): array {
		$parsed = self::parse_headers( $atts['headers'] ?? '' );
		$bare   = static function ( array $list ): array {
			return array_values(
				array_map(
					static function ( string $a ): string {
						return preg_match( '/<(.+)>/', $a, $m ) ? trim( $m[1] ) : trim( $a );
					},
					$list
				)
			);
		};
		$from   = (string) apply_filters( 'wp_mail_from', null !== $parsed['from_email'] ? $parsed['from_email'] : self::default_from() );
		$type   = (string) apply_filters( 'wp_mail_content_type', null !== $parsed['content_type'] ? $parsed['content_type'] : 'text/plain' );
		$body   = (string) ( $atts['message'] ?? '' );
		$files  = array_map( 'basename', self::list_of( $atts['attachments'] ?? array(), "\n" ) );
		$hdrs   = $parsed['headers'];
		if ( $files ) {
			$hdrs['X-Echo-Attachments'] = implode( ', ', $files );
		}
		$hdrs['Content-Type'] = $type;
		$reply                = $bare( $parsed['reply_to'] );
		$email                = array(
			'from'     => $from,
			'to'       => $bare( array_values( self::list_of( $atts['to'] ?? array(), ',' ) ) ),
			'cc'       => $bare( $parsed['cc'] ),
			'bcc'      => $bare( $parsed['bcc'] ),
			'reply_to' => $reply[0] ?? '',
			'subject'  => (string) ( $atts['subject'] ?? '' ),
			'headers'  => (object) $hdrs,
		);
		if ( 'text/html' === $type ) {
			$email['html'] = self::cap( $body, self::MAX_BODY );
		} else {
			$email['text'] = self::cap( $body, self::MAX_BODY );
		}
		return $email;
	}

	/**
	 * Fallback layer: another wp_mail() fired pre_wp_mail. Sealed, answer
	 * for the mailer so nothing is sent.
	 *
	 * @param mixed                $pre
	 * @param array<string, mixed> $atts
	 * @return mixed
	 */
	public static function pre_wp_mail( $pre, $atts ) {
		if ( null !== $pre || ! is_array( $atts ) ) {
			return $pre;
		}
		$d = self::send( self::capture( 'email', 'pre_wp_mail', array( 'email' => self::email_from_atts( $atts ) ) ) );
		if ( null === $d ) {
			if ( self::sealed() ) {
				do_action( 'wp_mail_failed', new WP_Error( 'wp_mail_failed', 'Rhythm Echo could not be reached and this site is sealed, so the email was not sent.', $atts ) );
				return false;
			}
			return null;
		}
		if ( 'allow' === ( $d['action'] ?? '' ) ) {
			self::$mail_reported = true;
			return null;
		}
		do_action( 'wp_mail_succeeded', $atts );
		return true;
	}

	/**
	 * Fallback layer: a wp_mail() that skipped pre_wp_mail but fired
	 * phpmailer_init. Sealed, the send is pointed at a sendmail that
	 * discards it (or, without one, left with no recipients so it fails).
	 *
	 * @param PHPMailer\PHPMailer\PHPMailer $phpmailer
	 */
	public static function phpmailer_init( $phpmailer ): void {
		if ( self::$mail_reported ) {
			self::$mail_reported = false;
			return;
		}
		if ( ! ( $phpmailer instanceof PHPMailer\PHPMailer\PHPMailer ) ) {
			return;
		}
		$d = self::send( self::capture( 'email', 'phpmailer_init', array( 'email' => self::email_from_phpmailer( $phpmailer ) ) ) );
		if ( null === $d ? ! self::sealed() : 'allow' === ( $d['action'] ?? '' ) ) {
			return;
		}
		if ( is_executable( '/bin/true' ) ) {
			$phpmailer->isSendmail();
			$phpmailer->Sendmail = '/bin/true';
			return;
		}
		$phpmailer->clearAllRecipients();
	}

	// ---------------------------------------------------------------------
	// Talking to Echo.

	/**
	 * A capture with the fields every capture has.
	 *
	 * @param array<string, mixed> $fields
	 * @return array<string, mixed>
	 */
	private static function capture( string $kind, string $hook, array $fields ): array {
		$host = (string) wp_parse_url( home_url(), PHP_URL_HOST );
		$port = wp_parse_url( home_url(), PHP_URL_PORT );
		$c    = array(
			'v'      => self::WIRE,
			'kind'   => $kind,
			'run'    => self::run(),
			'source' => array(
				'via'    => 'wordpress',
				'stack'  => 'wordpress',
				'plugin' => self::caller(),
				'hook'   => $hook,
				'host'   => $port ? $host . ':' . $port : $host,
			),
		);
		return array_merge( $c, $fields );
	}

	/**
	 * Posts a capture; returns Echo's decision, or null when Echo could not
	 * be reached or refused it.
	 *
	 * @param array<string, mixed> $capture
	 * @return array<string, mixed>|null
	 */
	private static function send( array $capture ): ?array {
		$body = wp_json_encode( $capture, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE | JSON_INVALID_UTF8_SUBSTITUTE );
		if ( ! is_string( $body ) ) {
			error_log( 'Rhythm Echo: could not encode a capture as JSON' ); // phpcs:ignore WordPress.PHP.DevelopmentFunctions
			return null;
		}
		list( $status, $out, $err ) = self::call( 'POST', '/v1/capture', $body );
		if ( 200 !== $status ) {
			error_log( sprintf( 'Rhythm Echo: %s %s/v1/capture failed (%s); %s', $capture['kind'], self::config()['url'], '' !== $err ? $err : 'HTTP ' . $status, self::sealed() ? 'sealed, so it was not sent' : 'observe mode, so it was sent anyway' ) ); // phpcs:ignore WordPress.PHP.DevelopmentFunctions
			return null;
		}
		$d = json_decode( $out, true );
		return is_array( $d ) ? $d : null;
	}

	/**
	 * One request to Echo, with a short timeout and no proxy. Raw cURL when
	 * it is there, so nothing else hooked into the WordPress HTTP API sees
	 * or changes Echo's own traffic; the HTTP API otherwise.
	 *
	 * @return array{0: int, 1: string, 2: string}
	 */
	private static function call( string $method, string $path, string $body = '' ): array {
		$cfg     = self::config();
		$url     = $cfg['url'] . $path;
		$headers = array( 'Content-Type: application/json', 'User-Agent: rhythm-echo-wordpress/' . self::VERSION );
		if ( '' !== $cfg['token'] ) {
			$headers[] = 'X-Echo-Token: ' . $cfg['token'];
		}
		if ( function_exists( 'curl_init' ) ) {
			$ch = curl_init( $url );
			if ( false === $ch ) {
				return array( 0, '', 'curl_init failed' );
			}
			curl_setopt_array(
				$ch,
				array(
					CURLOPT_CUSTOMREQUEST  => $method,
					CURLOPT_HTTPHEADER     => $headers,
					CURLOPT_RETURNTRANSFER => true,
					CURLOPT_CONNECTTIMEOUT => 2,
					CURLOPT_TIMEOUT        => 10,
					CURLOPT_PROXY          => '',
					CURLOPT_NOPROXY        => '*',
					CURLOPT_FOLLOWLOCATION => false,
				)
			);
			if ( 'POST' === $method ) {
				curl_setopt( $ch, CURLOPT_POSTFIELDS, $body );
			}
			$out    = curl_exec( $ch );
			$err    = curl_error( $ch );
			$status = (int) curl_getinfo( $ch, CURLINFO_RESPONSE_CODE );
			curl_close( $ch );
			return array( $status, is_string( $out ) ? $out : '', $err );
		}
		$flat = array();
		foreach ( $headers as $h ) {
			list( $k, $v ) = explode( ': ', $h, 2 );
			$flat[ $k ]    = $v;
		}
		self::$sending = true;
		try {
			$r = wp_remote_request(
				$url,
				array(
					'method'  => $method,
					'headers' => $flat,
					'body'    => 'POST' === $method ? $body : null,
					'timeout' => 10,
				)
			);
		} finally {
			self::$sending = false;
		}
		if ( is_wp_error( $r ) ) {
			return array( 0, '', $r->get_error_message() );
		}
		return array( (int) wp_remote_retrieve_response_code( $r ), (string) wp_remote_retrieve_body( $r ), '' );
	}

	/**
	 * The plugin that made the call: the first frame in plugins/<slug>/,
	 * a mu-plugin or a theme.
	 */
	private static function caller(): string {
		$plugins = wp_normalize_path( WP_PLUGIN_DIR ) . '/';
		$mu      = wp_normalize_path( WPMU_PLUGIN_DIR ) . '/';
		$themes  = wp_normalize_path( get_theme_root() ) . '/';
		$self    = wp_normalize_path( __FILE__ );
		foreach ( debug_backtrace( DEBUG_BACKTRACE_IGNORE_ARGS, 60 ) as $frame ) { // phpcs:ignore WordPress.PHP.DevelopmentFunctions
			if ( empty( $frame['file'] ) ) {
				continue;
			}
			$file = wp_normalize_path( $frame['file'] );
			if ( $file === $self ) {
				continue;
			}
			foreach ( array( '' => $plugins, 'mu-plugin:' => $mu, 'theme:' => $themes ) as $prefix => $dir ) {
				if ( 0 === strpos( $file, $dir ) ) {
					$rest = substr( $file, strlen( $dir ) );
					$slug = explode( '/', $rest )[0];
					return $prefix . preg_replace( '/\.php$/', '', $slug );
				}
			}
		}
		return '';
	}

	private static function filled( string $v ): bool {
		return '' !== $v;
	}

	private static function cap( string $v, int $max ): string {
		return strlen( $v ) > $max ? substr( $v, 0, $max ) : $v;
	}

	private static function relative( string $path ): string {
		$abs = wp_normalize_path( ABSPATH );
		$p   = wp_normalize_path( $path );
		return 0 === strpos( $p, $abs ) ? substr( $p, strlen( $abs ) ) : $p;
	}

	// ---------------------------------------------------------------------
	// REST routes for `rhythm echo doctor`.

	public static function routes(): void {
		register_rest_route(
			'rhythm-echo/v1',
			'/ping',
			array(
				'methods'             => array( 'GET', 'POST' ),
				'callback'            => array( __CLASS__, 'ping' ),
				'permission_callback' => array( __CLASS__, 'allowed' ),
			)
		);
		register_rest_route(
			'rhythm-echo/v1',
			'/canary',
			array(
				'methods'             => 'POST',
				'callback'            => array( __CLASS__, 'canary' ),
				'permission_callback' => array( __CLASS__, 'allowed' ),
			)
		);
	}

	/**
	 * With a token configured, only X-Echo-Token opens the routes. Without
	 * one, an administrator or a request from this machine or a private
	 * network (Echo on the Docker host) may.
	 *
	 * @param WP_REST_Request<array<string, mixed>> $request
	 */
	public static function allowed( $request ): bool {
		$token = self::config()['token'];
		if ( '' !== $token ) {
			$given = (string) $request->get_header( 'X-Echo-Token' );
			return '' !== $given && hash_equals( $token, $given );
		}
		if ( current_user_can( 'manage_options' ) ) {
			return true;
		}
		$ip = isset( $_SERVER['REMOTE_ADDR'] ) ? (string) $_SERVER['REMOTE_ADDR'] : ''; // phpcs:ignore WordPress.Security.ValidatedSanitizedInput
		return '' !== $ip && false === filter_var( $ip, FILTER_VALIDATE_IP, FILTER_FLAG_NO_PRIV_RANGE | FILTER_FLAG_NO_RES_RANGE );
	}

	/**
	 * What `rhythm echo doctor` needs to know: whether the connector runs, which
	 * email layer it uses, and whether this site can reach Echo.
	 *
	 * @return array<string, mixed>
	 */
	public static function ping(): array {
		$cfg = self::config();
		list( $status, $body, $err ) = self::call( 'GET', '/v1/health' );
		$health                      = json_decode( $body, true );
		return array(
			'ok'          => true,
			'connector'   => 'wordpress',
			'version'     => self::VERSION,
			'wire'        => self::WIRE,
			'active'      => self::$active,
			'reason'      => self::$reason,
			'environment' => self::environment(),
			'mode'        => $cfg['mode'],
			'layers'      => self::$active ? array_values( array_filter( array( self::$layer, 'pre_http_request', '' !== $cfg['proxy'] ? 'proxy' : '' ) ) ) : array(),
			'proxy'       => self::$active ? $cfg['proxy'] : '',
			'ca'          => '' !== $cfg['ca'] && is_readable( $cfg['ca'] ),
			'warnings'    => self::$warnings,
			'site'        => home_url(),
			'run'         => self::run(),
			'echo'        => array(
				'url'       => $cfg['url'],
				'reachable' => 200 === $status,
				'error'     => 200 === $status ? '' : ( '' !== $err ? $err : 'HTTP ' . $status ),
				'mode'      => is_array( $health ) && isset( $health['mode'] ) ? (string) $health['mode'] : '',
			),
		);
	}

	/**
	 * Sends one email and one HTTP request to addresses that can't exist,
	 * so doctor can check both layers catch them.
	 *
	 * @return array<string, mixed>|WP_Error
	 */
	public static function canary() {
		if ( ! self::$active ) {
			return new WP_Error( 'rhythm_echo_inactive', 'Rhythm Echo is not running on this site: ' . self::$reason, array( 'status' => 409 ) );
		}
		$id    = wp_generate_password( 12, false );
		$error = '';
		$fail  = static function ( $e ) use ( &$error ) {
			if ( $e instanceof WP_Error ) {
				$error = $e->get_error_message();
			}
		};
		add_action( 'wp_mail_failed', $fail );
		$mail = wp_mail( self::CANARY_EMAIL, 'Rhythm Echo canary ' . $id, "This is a Rhythm Echo canary. If you can read this, it was not caught.\n\nCanary: " . $id, array( 'From: Rhythm Echo <' . self::CANARY_EMAIL . '>' ) );
		remove_action( 'wp_mail_failed', $fail );
		$r    = wp_remote_post(
			self::CANARY_URL,
			array(
				'headers' => array( 'Content-Type' => 'application/json' ),
				'body'    => wp_json_encode( array( 'canary' => $id ) ),
				'timeout' => 5,
			)
		);
		$http = is_wp_error( $r )
			? array( 'error' => $r->get_error_message() )
			: array(
				'status' => (int) wp_remote_retrieve_response_code( $r ),
				'body'   => self::cap( (string) wp_remote_retrieve_body( $r ), 2048 ),
			);
		return array(
			'canary' => $id,
			'email'  => array(
				'to'    => self::CANARY_EMAIL,
				'sent'  => (bool) $mail,
				'error' => $error,
			),
			'http'   => array_merge( array( 'url' => self::CANARY_URL ), $http ),
		);
	}
}

Rhythm_Echo::boot();

if ( Rhythm_Echo::active() ) {
	if ( ! function_exists( 'wp_mail' ) ) {
		/**
		 * Rhythm Echo's wp_mail(): see Rhythm_Echo::wp_mail().
		 *
		 * @param string|string[] $to
		 * @param string          $subject
		 * @param string          $message
		 * @param string|string[] $headers
		 * @param string|string[] $attachments
		 * @param string|string[] $embeds
		 */
		function wp_mail( $to, $subject, $message, $headers = '', $attachments = array(), $embeds = array() ): bool {
			return Rhythm_Echo::wp_mail( $to, $subject, $message, $headers, $attachments, $embeds );
		}
		Rhythm_Echo::use_layer( 'wp_mail' );
	} else {
		Rhythm_Echo::use_layer( 'pre_wp_mail' );
	}
}
