<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

namespace Rhythm\Echo\Tests;

use Illuminate\Support\Facades\Http;
use Illuminate\Support\Facades\Mail;
use Illuminate\Support\Facades\Route;
use Rhythm\Echo\Connector;
use Rhythm\Echo\EchoClient;
use Rhythm\Echo\Tests\Fixtures\OrderShipped;
use Rhythm\Echo\Wire;

/**
 * X-Echo-Run counts only when signed with the token, or with no token when
 * sent straight from this machine. The vectors are shared with Echo's wire
 * package and the other connectors (conformance/testdata in the Echo
 * repository; ECHO_CONFORMANCE names the folder when it lives elsewhere).
 */
class RunSignatureTest extends TestCase
{
    public const SIGNED = 'r1/checkout.NhOp3HY0JQ76wSoPnn3sZg';

    protected function defineRoutes($router): void
    {
        Route::get('/contact', function () {
            Mail::to('ada@example.test')->send(new OrderShipped);
            Http::post('https://api.twilio.com/2010-04-01/Accounts/AC0/Messages.json');

            return 'sent';
        });
    }

    /** @return array<string, mixed> */
    private static function vectors(): array
    {
        $dir = getenv('ECHO_CONFORMANCE') ?: __DIR__.'/../../../conformance';
        $path = $dir.'/testdata/run-signature.json';
        if (! is_readable($path)) {
            self::markTestSkipped("no shared vectors at {$path}");
        }

        return json_decode((string) file_get_contents($path), true, flags: JSON_THROW_ON_ERROR);
    }

    /** @return list<string> */
    private function runs(): array
    {
        return array_map(fn ($c) => $c['run'] ?? '', $this->captures());
    }

    public function test_the_signature_matches_echo(): void
    {
        $this->assertSame(self::SIGNED, Wire::signRun('secret', 'r1/checkout'));
        $this->assertSame(['r1', 'checkout', true], Wire::verifyRun('secret', self::SIGNED));
        $this->assertSame('r1/checkout', Wire::signRun('', 'r1/checkout'));
    }

    public function test_verify_vectors(): void
    {
        foreach (self::vectors()['verify'] as $v) {
            $this->assertSame([$v['run'], $v['action'], $v['ok']], Wire::verifyRun($v['token'], $v['value']), $v['name']);
        }
    }

    public function test_accept_vectors(): void
    {
        foreach (self::vectors()['accept'] as $v) {
            $echo = new Connector($this->app, $this->app->make(EchoClient::class), ['token' => $v['token']]);
            $got = $echo->acceptRun($v['value'], $v['remote'], $v['forwarded']);
            $this->assertSame($v['run'], $got, $v['name']);
        }
    }

    public function test_without_a_token_an_unsigned_value_from_a_public_ip_is_ignored(): void
    {
        $this->withServerVariables(['REMOTE_ADDR' => '203.0.113.9'])
            ->withHeader('X-Echo-Run', 'r1/checkout')->get('/contact')->assertOk();

        $this->assertSame(['', ''], $this->runs());
    }

    public function test_without_a_token_a_proxied_value_is_ignored(): void
    {
        $this->withHeader('X-Forwarded-For', '203.0.113.9')
            ->withHeader('X-Echo-Run', 'r1/checkout')->get('/contact')->assertOk();

        $this->assertSame(['', ''], $this->runs());
    }

    public function test_the_environment_drops_a_signature(): void
    {
        putenv('RHYTHM_ECHO_RUN='.self::SIGNED);
        Mail::raw('Hi', fn ($m) => $m->to('a@example.test')->subject('CLI'));

        $this->assertSame(['r1/checkout'], $this->runs());
    }
}
