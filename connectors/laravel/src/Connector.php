<?php

namespace Rhythm\Echo;

use Illuminate\Contracts\Foundation\Application;
use Illuminate\Log\Context\Repository as Context;
use Illuminate\Support\Env;

/**
 * The connector's state: whether it runs, where Echo is, which run the
 * current work belongs to, and the envelope every capture shares.
 */
class Connector
{
    /** Hidden Context key: hidden so the run never lands in log lines. */
    public const CONTEXT_KEY = 'rhythm_echo.run';

    /** Header names whose values are credentials, masked before sending. */
    private const SECRET_HEADERS = [
        'authorization', 'proxy-authorization', 'cookie', 'set-cookie', 'x-api-key', 'api-key',
        'x-auth-token', 'x-postmark-server-token', 'x-echo-token', 'x-vonage-signature',
    ];

    private ?string $reason = null;

    public function __construct(
        private readonly Application $app,
        private readonly EchoClient $client,
        /** @var array<string, mixed> */
        private readonly array $config,
    ) {
    }

    public function client(): EchoClient
    {
        return $this->client;
    }

    /** Whether the connector captures anything at all. */
    public function active(): bool
    {
        return $this->inactiveReason() === null;
    }

    /** Why the connector is off, or null when it is on. */
    public function inactiveReason(): ?string
    {
        if ($this->reason !== null) {
            return $this->reason === '' ? null : $this->reason;
        }
        $env = (string) $this->app->environment();
        $enabled = $this->config['enabled'] ?? null;
        if ($enabled === '') {
            $enabled = null; // RHYTHM_ECHO_ENABLED= in .env means unset
        }
        $force = filter_var($this->config['force'] ?? false, FILTER_VALIDATE_BOOL);
        $this->reason = match (true) {
            $env === 'production' && ! $force => 'APP_ENV is production (set RHYTHM_ECHO_FORCE=true only on a box that is not)',
            $enabled === null && $env === 'testing' => 'APP_ENV is testing (set RHYTHM_ECHO_ENABLED=true to capture in tests)',
            $enabled !== null && ! filter_var($enabled, FILTER_VALIDATE_BOOL) => 'RHYTHM_ECHO_ENABLED is false',
            default => '',
        };

        return $this->reason === '' ? null : $this->reason;
    }

    /**
     * Whether to fail closed when Echo can't be reached. Echo's own mode
     * decides everything else; this is only the fallback.
     */
    public function sealed(): bool
    {
        return ($this->config['mode'] ?? Wire::SEAL) !== Wire::OBSERVE;
    }

    /**
     * The "<run>/<action>" the current work belongs to: the X-Echo-Run
     * header or cookie of the request (put into Context by TrackRun), the
     * Context a queued job carried over from that request, or else
     * RHYTHM_ECHO_RUN, which Echo sets on commands it runs.
     */
    public function run(): string
    {
        $ctx = $this->context()?->getHidden(self::CONTEXT_KEY);
        if (is_string($ctx) && $ctx !== '') {
            return $ctx;
        }

        return self::cleanRun(Env::get(Wire::RUN_ENV));
    }

    /** Stores the run for this request and every job it queues. */
    public function remember(string $run): void
    {
        $this->context()?->addHidden(self::CONTEXT_KEY, $run);
    }

    /**
     * Keeps a run value short and printable: it comes from a request.
     */
    public static function cleanRun(mixed $v): string
    {
        if (! is_string($v)) {
            return '';
        }
        $v = trim(rawurldecode($v));
        if ($v === '' || strlen($v) > 256 || preg_match('/[\x00-\x1f\x7f]/', $v)) {
            return '';
        }

        return $v;
    }

    /**
     * The site's own host, which Echo always lets through (loopback and
     * cron-style calls to itself).
     */
    public function siteHost(): string
    {
        if (! $this->app->runningInConsole() && $this->app->bound('request')) {
            $host = (string) $this->app->make('request')->getHost();
            if ($host !== '') {
                return $host;
            }
        }
        $url = $this->app->make('config')->get('app.url');

        return is_string($url) ? (string) parse_url($url, PHP_URL_HOST) : '';
    }

    /**
     * Sends a capture with the shared envelope filled in.
     *
     * @param  array<string, mixed>  $capture  kind, phase, email/http…
     * @param  array<string, string>  $source  plugin, hook
     * @return array{id: string, mode: string, action: string, response?: array{status: int, headers?: array<string, string>, body?: string}}
     *
     * @throws EchoUnreachable
     */
    public function capture(array $capture, array $source): array
    {
        $capture = ['v' => Wire::VERSION] + $capture;
        $run = $this->run();
        if ($run !== '') {
            $capture['run'] = $run;
        }
        $capture['source'] = array_filter([
            'via' => 'laravel',
            'stack' => 'laravel',
            'plugin' => $source['plugin'] ?? '',
            'hook' => $source['hook'] ?? '',
            'host' => $this->siteHost(),
        ], fn ($v) => $v !== '');

        return $this->client->capture($capture);
    }

    /**
     * Flattens headers and masks credentials, so captures never carry them.
     *
     * @param  array<string, string|array<string>>  $headers
     * @return array<string, string>
     */
    public static function headers(array $headers): array
    {
        $out = [];
        foreach ($headers as $name => $value) {
            $value = is_array($value) ? implode(', ', $value) : (string) $value;
            $out[(string) $name] = in_array(strtolower((string) $name), self::SECRET_HEADERS, true) ? '•••••' : $value;
        }

        return $out;
    }

    /**
     * Best guess at the package that made a call: the first frame
     * that isn't plumbing (the framework, Guzzle, Symfony). Empty when
     * that frame is the app's own code.
     */
    public static function caller(): string
    {
        $skip = ['laravel/framework', 'rhythm/echo-laravel', 'laravel/serializable-closure'];
        $skipVendors = ['guzzlehttp', 'symfony', 'psr', 'php-http', 'nesbot', 'illuminate'];
        foreach (debug_backtrace(DEBUG_BACKTRACE_IGNORE_ARGS, 64) as $frame) {
            $file = str_replace('\\', '/', $frame['file'] ?? '');
            if ($file === '' || str_starts_with($file, str_replace('\\', '/', __DIR__))) {
                continue;
            }
            if (! preg_match('#/vendor/([^/]+)/([^/]+)/#', $file, $m)) {
                return ''; // the app's own code
            }
            if (in_array($m[1], $skipVendors, true) || in_array("{$m[1]}/{$m[2]}", $skip, true)) {
                continue;
            }

            return "{$m[1]}/{$m[2]}";
        }

        return '';
    }

    private function context(): ?Context
    {
        return $this->app->bound(Context::class) ? $this->app->make(Context::class) : null;
    }
}
