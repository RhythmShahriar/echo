<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

namespace Rhythm\Echo;

use GuzzleHttp\Client;
use GuzzleHttp\RequestOptions;
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
     * @param  callable|null  $handler  a Guzzle handler, for tests
     */
    public function __construct(
        private readonly string $url,
        private readonly ?string $token = null,
        float $timeout = 2.0,
        ?callable $handler = null,
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
        $decision = $this->call('POST', 'v1/capture', $capture);

        return [
            'id' => (string) ($decision['id'] ?? ''),
            'mode' => (string) ($decision['mode'] ?? ''),
            'action' => (string) ($decision['action'] ?? ''),
        ] + (is_array($decision['response'] ?? null) ? ['response' => $this->response($decision['response'])] : []);
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
            throw new EchoUnreachable("Rhythm Echo at {$this->url} answered {$response->getStatusCode()}: ".trim(substr($body, 0, 200)));
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
