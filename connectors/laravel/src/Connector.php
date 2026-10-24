<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

namespace Rhythm\Echo;

use Illuminate\Contracts\Cache\Repository;
use Illuminate\Contracts\Foundation\Application;
use Illuminate\Log\Context\Repository as Context;
use Illuminate\Support\Env;
use Throwable;

/**
 * The connector's state: whether it runs, where Echo is, which run the
 * current work belongs to, and the envelope every capture shares.
 */
class Connector
{
    /** Hidden Context key: hidden so the run never lands in log lines. */
    public const CONTEXT_KEY = 'rhythm_echo.run';

    /**
     * Header names whose whole value is a credential. The same rule as
     * Echo's catcher, so a header let through here is not stored raw there.
     */
    private const SECRET_HEADER = '/token|secret|key|auth|signature|session|password|cookie|credential/i';

    /**
     * A Stripe key's mode prefix. It carries no secret and is what Echo
     * reads to fake a test-mode payment call or refuse a live one.
     */
    private const KEY_MODE = '/^[spr]k_(?:live|test)_/';

    private ?string $reason = null;

    /** How long, in seconds, an ignored X-Echo-Run stays quiet after it was logged. */
    public const RUN_LOG_QUIET = 600;

    /**
     * Reasons an ignored X-Echo-Run was logged for in this process.
     *
     * @var array<string, true>
     */
    private array $runLogged = [];

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
     * header or cookie of the request (checked and put into Context by
     * TrackRun), the Context a queued job carried over from that request,
     * or else RHYTHM_ECHO_RUN, which Echo sets on commands it runs. A
     * visitor cannot set the environment, so it is taken as given, without
     * a signature if Echo added one.
     */
    public function run(): string
    {
        $ctx = $this->context()?->getHidden(self::CONTEXT_KEY);
        if (is_string($ctx) && $ctx !== '') {
            return $ctx;
        }

        return Wire::splitSignature(self::cleanRun(Env::get(Wire::RUN_ENV)))[0];
    }

    /**
     * Whether a request's X-Echo-Run counts. A visitor can send one too and
     * add messages to a baseline or drop them from one, so it must be signed
     * with the token when one is set, or else come straight from this
     * machine (loopback, no forwarding headers). Returns "<run>/<action>"
     * without the signature, or "" for a value that does not count, logged
     * once.
     */
    public function acceptRun(string $value, string $remote, bool $forwarded): string
    {
        $token = $this->runToken();
        [$run, $action, $ok] = Wire::verifyRun($token, $value);
        if ($run === '') {
            $this->runIgnored('bad', 'ignored an X-Echo-Run whose signature does not verify ('.($token !== '' ? 'not signed with this token' : 'RHYTHM_ECHO_TOKEN is not set here').')');

            return '';
        }
        if ($ok) {
            return "{$run}/{$action}";
        }
        if ($token !== '') {
            $this->runIgnored('unsigned', 'ignored an unsigned X-Echo-Run: with RHYTHM_ECHO_TOKEN set, only runs Echo signed with it count');

            return '';
        }
        if (! $forwarded && ($remote === '::1' || preg_match('/^(::ffff:)?127\.\d{1,3}\.\d{1,3}\.\d{1,3}$/i', $remote))) {
            return "{$run}/{$action}";
        }
        $this->runIgnored('remote', sprintf(
            'ignored an X-Echo-Run from %s%s: without RHYTHM_ECHO_TOKEN only a direct call from this machine may set the run (set the same token here and in Echo)',
            $remote !== '' ? $remote : 'an unknown address', $forwarded ? ' behind a proxy' : '',
        ));

        return '';
    }

    /**
     * What X-Echo-Run signatures are checked with: RHYTHM_ECHO_TOKEN, else
     * the write token (Echo signs a hub site's runs with that site's token).
     */
    private function runToken(): string
    {
        return (string) (($this->config['token'] ?? '') ?: ($this->config['write_token'] ?? ''));
    }

    /**
     * Logs why an X-Echo-Run was ignored, once per reason every ten
     * minutes. Under PHP-FPM every request is a new process, so the
     * property alone would let any visitor add a log line per request with
     * a forged header; the app's cache remembers it across requests.
     */
    private function runIgnored(string $reason, string $message): void
    {
        if (isset($this->runLogged[$reason])) {
            return;
        }
        $this->runLogged[$reason] = true;
        try {
            $cache = $this->app->bound('cache') ? $this->app->make('cache')->store() : null;
            $first = ! $cache instanceof Repository || $cache->add('rhythm-echo.runlog.'.$reason, 1, self::RUN_LOG_QUIET);
        } catch (Throwable) {
            $first = true; // a broken cache costs a log line, never the request
        }
        if ($first) {
            $this->app->make('log')->warning('Rhythm Echo: '.$message);
        }
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
     * cron-style calls to itself). It is the host of APP_URL, never the
     * request's Host: Laravel does not trust hosts by default, so a visitor
     * who sent Host: api.twilio.com would have unsealed Twilio.
     */
    public function siteHost(): string
    {
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
            'site' => trim((string) ($this->config['site_id'] ?? '')),
        ], fn ($v) => $v !== '');

        $wire = $this->client->wire();
        $decision = $this->client->capture($capture);
        if ($this->client->wire() !== $wire) {
            $this->app->make('log')->warning("Rhythm Echo: Echo at {$this->client->url()} speaks wire {$this->client->wire()}: sending that (rhythm update gets a newer Echo)");
        }

        return $decision;
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
            $out[(string) $name] = self::maskHeader((string) $name, $value);
        }

        return $out;
    }

    /**
     * One header's value as a capture may carry it. Authorization keeps
     * its scheme. Basic is decoded so the user name (a Stripe key
     * there) keeps its mode prefix.
     */
    public static function maskHeader(string $name, string $value): string
    {
        if ($value === '' || ! preg_match(self::SECRET_HEADER, $name)) {
            return $value;
        }
        if (! preg_match('/^(?:proxy-)?authorization$/i', $name)
            || ! preg_match('/^([A-Za-z][A-Za-z0-9-]*)\s+(\S+)$/', trim($value), $m)) {
            return self::mask($value);
        }
        if (strtolower($m[1]) === 'basic') {
            $plain = base64_decode($m[2], true);
            if ($plain !== false && str_contains($plain, ':')) {
                return $m[1].' '.self::mask(explode(':', $plain, 2)[0]);
            }
        }

        return $m[1].' '.self::mask($m[2]);
    }

    /**
     * Hides a credential whole. Only a Stripe key's mode prefix stays
     * ("sk_test_•••••"); an empty value stays empty.
     */
    public static function mask(string $value): string
    {
        if ($value === '') {
            return '';
        }

        return (preg_match(self::KEY_MODE, $value, $m) ? $m[0] : '').'•••••';
    }

    /**
     * Best guess at the package that made a call: the first frame
     * that isn't plumbing (the framework, Guzzle, Symfony). Empty when
     * that frame is the app's own code.
     */
    public static function caller(): string
    {
        $skip = ['laravel/framework', 'rhythmsh/echo-laravel', 'laravel/serializable-closure'];
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
