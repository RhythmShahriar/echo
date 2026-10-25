<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

namespace Rhythm\Echo;

use Closure;
use GuzzleHttp\Client;
use GuzzleHttp\RequestOptions;
use Illuminate\Contracts\Cache\Repository;
use Throwable;

/**
 * Talks to Echo. It is a bare Guzzle client built here, never through the
 * Http facade or the container, so it can't pass through the connector's
 * own middleware and loop. It also ignores HTTPS_PROXY: Echo's proxy must
 * not see Echo's own API calls.
 */
class EchoClient
{
    private Client $http;

    /**
     * How long an older wire version is kept before the newest is tried
     * again, in seconds. A queue worker outlives an Echo upgrade and would
     * otherwise keep dropping what the older wire lacks.
     */
    public const WIRE_RECHECK = 3600;

    /**
     * The wire version each Echo speaks, by URL, once one refused a newer
     * capture, with when that was learned. Kept for the process, so a queue
     * worker asks about once an hour. It is also put in the app's cache:
     * under PHP-FPM every request is a new process and would otherwise pay
     * a refused capture and a health call before each real one.
     *
     * @var array<string, array{0: int, 1: int}>
     */
    private static array $wire = [];

    /**
     * @param  callable|null  $handler  a Guzzle handler, for tests
     * @param  (Closure(): ?Repository)|null  $cache  the cache store the wire version is remembered in, resolved when first needed
     */
    public function __construct(
        private readonly string $url,
        private readonly ?string $token = null,
        float $timeout = 2.0,
        ?callable $handler = null,
        private readonly ?Closure $cache = null,
    ) {
        $options = [
            'base_uri' => rtrim($url, '/').'/',
            RequestOptions::TIMEOUT => $timeout,
            RequestOptions::CONNECT_TIMEOUT => min($timeout, 1.0),
            RequestOptions::HTTP_ERRORS => false,
            // An empty string disables the proxy, including curl's own
            // http_proxy environment lookup.
            RequestOptions::PROXY => '',
            RequestOptions::ALLOW_REDIRECTS => false,
        ];
        if ($handler !== null) {
            $options['handler'] = $handler;
        }
        $this->http = new Client($options);
    }

    /** Echo's base URL, as configured. */
    public function url(): string
    {
        return $this->url;
    }

    /**
     * Posts a capture and returns Echo's decision.
     *
     * @param  array<string, mixed>  $capture
     * @return array{id: string, mode: string, action: string, response?: array{status: int, headers?: array<string, string>, body?: string}}
     *
     * @throws EchoUnreachable
     */
    public function capture(array $capture): array
    {
        $version = $this->wire();
        try {
            $decision = $this->call('POST', 'v1/capture', Wire::forVersion($capture, $version));
        } catch (EchoUnreachable $e) {
            // An older Echo refuses a newer wire version: ask which it
            // speaks and send that from now on.
            $older = $e->status === 400 && str_contains($e->getMessage(), 'newer than Echo') && $version === Wire::VERSION ? $this->olderWire() : null;
            if ($older === null) {
                throw $e;
            }
            self::$wire[$this->url] = [$older, time()];
            $this->cached(fn (Repository $c) => $c->put($this->wireKey(), self::$wire[$this->url], self::WIRE_RECHECK));
            $decision = $this->call('POST', 'v1/capture', Wire::forVersion($capture, $older));
        }

        return [
            'id' => (string) ($decision['id'] ?? ''),
            'mode' => (string) ($decision['mode'] ?? ''),
            'action' => (string) ($decision['action'] ?? ''),
        ] + (is_array($decision['response'] ?? null) ? ['response' => $this->response($decision['response'])] : []);
    }

    /**
     * The wire version /v1/health names, when it is one this connector can
     * fall back to.
     */
    private function olderWire(): ?int
    {
        try {
            $wire = $this->health()['wire'] ?? null;
        } catch (EchoUnreachable) {
            return null;
        }

        return is_int($wire) && $wire >= Wire::MIN_VERSION && $wire < Wire::VERSION ? $wire : null;
    }

    /**
     * The wire version captures go to this Echo in.
     */
    public function wire(): int
    {
        if (! isset(self::$wire[$this->url])) {
            $learned = $this->cached(fn (Repository $c) => $c->get($this->wireKey()));
            if (is_array($learned) && is_int($learned[0] ?? null) && is_int($learned[1] ?? null)
                && $learned[0] >= Wire::MIN_VERSION && $learned[0] < Wire::VERSION) {
                self::$wire[$this->url] = [$learned[0], $learned[1]];
            }
        }
        [$version, $at] = self::$wire[$this->url] ?? [Wire::VERSION, 0];
        if ($version !== Wire::VERSION && time() - $at >= self::WIRE_RECHECK) {
            unset(self::$wire[$this->url]);

            return Wire::VERSION;
        }

        return $version;
    }

    /** The cache key for this Echo's wire version: another Echo may be newer. */
    private function wireKey(): string
    {
        return 'rhythm-echo.wire.'.md5($this->url);
    }

    /**
     * Runs $fn on the cache store, or returns null without one. A broken
     * cache (a database store whose table is missing, Redis down) must
     * never stop a capture: the static above still works for the process.
     *
     * @param  Closure(Repository): mixed  $fn
     */
    private function cached(Closure $fn): mixed
    {
        try {
            $store = $this->cache !== null ? ($this->cache)() : null;

            return $store !== null ? $fn($store) : null;
        } catch (Throwable) {
            return null;
        }
    }

    /**
     * GET /v1/health.
     *
     * @return array<string, mixed>
     *
     * @throws EchoUnreachable
     */
    public function health(): array
    {
        return $this->call('GET', 'v1/health');
    }

    /**
     * @param  array<string, mixed>|null  $json
     * @return array<string, mixed>
     */
    private function call(string $method, string $path, ?array $json = null): array
    {
        $options = ['headers' => ['Accept' => 'application/json', 'User-Agent' => 'rhythm-echo-laravel']];
        if ($this->token !== null && $this->token !== '') {
            $options['headers'][Wire::TOKEN_HEADER] = $this->token;
        }
        if ($json !== null) {
            $options[RequestOptions::JSON] = $json;
        }
        try {
            $response = $this->http->request($method, $path, $options);
        } catch (Throwable $e) {
            throw new EchoUnreachable("Rhythm Echo is unreachable at {$this->url}: ".$e->getMessage(), 0, $e);
        }
        $body = (string) $response->getBody();
        if ($response->getStatusCode() !== 200) {
            $e = new EchoUnreachable("Rhythm Echo at {$this->url} answered {$response->getStatusCode()}: ".trim(substr($body, 0, 200)));
            $e->status = $response->getStatusCode();
            throw $e;
        }
        $data = json_decode($body, true);
        if (! is_array($data)) {
            throw new EchoUnreachable("Rhythm Echo at {$this->url} answered something that isn't JSON");
        }

        return $data;
    }

    /**
     * @param  array<mixed>  $r
     * @return array{status: int, headers: array<string, string>, body: string}
     */
    private function response(array $r): array
    {
        $headers = [];
        foreach ((array) ($r['headers'] ?? []) as $k => $v) {
            $headers[(string) $k] = (string) $v;
        }

        return ['status' => (int) ($r['status'] ?? 200) ?: 200, 'headers' => $headers, 'body' => (string) ($r['body'] ?? '')];
    }
}
