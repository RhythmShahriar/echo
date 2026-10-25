<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

namespace Rhythm\Echo\Http;

use Closure;
use GuzzleHttp\Exception\ConnectException;
use GuzzleHttp\Promise\Create;
use GuzzleHttp\Promise\FulfilledPromise;
use GuzzleHttp\Promise\PromiseInterface;
use GuzzleHttp\Psr7\Response;
use Psr\Http\Message\MessageInterface;
use Psr\Http\Message\RequestInterface;
use Psr\Http\Message\ResponseInterface;
use Rhythm\Echo\Connector;
use Rhythm\Echo\EchoUnreachable;
use Rhythm\Echo\Wire;
use Throwable;

/**
 * Guzzle middleware that reports each outbound request to Echo and obeys
 * its decision: fake answers with Echo's response without sending, block
 * fails the request like a dead connection, allow sends it and reports the
 * response after.
 *
 * Laravel turns the ConnectException into its own ConnectionException, so
 * blocked calls look like any network failure to the app.
 */
final class CaptureRequests
{
    /** Bytes of a request or response body sent to Echo; it keeps 256 KiB. */
    private const MAX_BODY = 256 << 10;

    /**
     * @param  string  $hook  reported as source.hook: http for the Http client, guzzle for container-built clients
     */
    public function __construct(private readonly Connector $echo, private readonly string $hook = 'http')
    {
    }

    /**
     * Wraps the next handler. With Echo unreachable, a sealed site's
     * request is rejected as a connection failure rather than sent:
     * staging must never reach a real service because Echo is down.
     */
    public function __invoke(callable $handler): Closure
    {
        return function (RequestInterface $request, array $options) use ($handler): PromiseInterface {
            if (! $this->echo->active() || $this->isEcho($request)) {
                return $handler($request, $options);
            }
            $http = [
                'method' => $request->getMethod(),
                'url' => (string) $request->getUri(),
                'headers' => Connector::headers($request->getHeaders()),
                'body' => self::body($request),
            ];
            $source = ['plugin' => Connector::caller(), 'hook' => $this->hook];
            try {
                $d = $this->echo->capture(['kind' => 'http', 'phase' => Wire::BEFORE, 'http' => array_filter($http)], $source);
            } catch (EchoUnreachable $e) {
                if ($this->echo->sealed()) {
                    return Create::rejectionFor(new ConnectException($e->sealedMessage('request'), $request, $e));
                }

                return $handler($request, $options);
            }

            return match ($d['action']) {
                Wire::FAKE => new FulfilledPromise(new Response(
                    $d['response']['status'] ?? 200, $d['response']['headers'] ?? [], $d['response']['body'] ?? '')),
                Wire::BLOCK => Create::rejectionFor(new ConnectException('Rhythm Echo blocked this request', $request)),
                default => $this->send($handler, $request, $options, $d['id'], $http, $source),
            };
        };
    }

    /**
     * Sends for real, then reports the response with the same ID.
     *
     * @param  array<string, mixed>  $options
     * @param  array<string, mixed>  $http
     * @param  array<string, string>  $source
     */
    private function send(callable $handler, RequestInterface $request, array $options, string $id, array $http, array $source): PromiseInterface
    {
        $after = function (?ResponseInterface $response, string $error) use ($id, $http, $source, $options): void {
            $http['status'] = $response?->getStatusCode() ?? 0;
            if ($response !== null && empty($options['stream'])) {
                $http['response'] = self::body($response);
            }
            $capture = ['kind' => 'http', 'phase' => Wire::AFTER, 'id' => $id, 'http' => array_filter($http)];
            if ($error !== '') {
                $capture['error'] = $error;
            }
            try {
                $this->echo->capture($capture, $source);
            } catch (EchoUnreachable) {
                // It was sent already; the after phase only records the answer.
            }
        };

        return $handler($request, $options)->then(
            function (ResponseInterface $response) use ($after) {
                $after($response, '');

                return $response;
            },
            function (mixed $reason) use ($after) {
                $after(null, $reason instanceof Throwable ? $reason->getMessage() : 'request failed');

                return Create::rejectionFor($reason);
            },
        );
    }

    /** Calls to Echo's own API pass untouched. */
    private function isEcho(RequestInterface $request): bool
    {
        $echo = parse_url($this->echo->client()->url());
        $uri = $request->getUri();

        return strcasecmp($uri->getHost(), (string) ($echo['host'] ?? '')) === 0
            && ($uri->getPort() ?? 0) === ($echo['port'] ?? 0)
            && str_starts_with($uri->getPath(), '/v1/');
    }

    /**
     * Reads up to MAX_BODY of a body and rewinds it. Streams that can't be
     * rewound are left alone: reading them would eat the body.
     */
    private static function body(MessageInterface $message): string
    {
        $body = $message->getBody();
        if (! $body->isSeekable()) {
            return '';
        }
        try {
            $pos = $body->tell();
            $body->rewind();
            $s = $body->read(self::MAX_BODY);
            $body->seek($pos);

            return $s;
        } catch (Throwable) {
            return '';
        }
    }
}
