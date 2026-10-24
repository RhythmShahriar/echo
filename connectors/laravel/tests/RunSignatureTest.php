<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

namespace Rhythm\Echo\Tests;

use Illuminate\Support\Facades\Cache;
use Illuminate\Support\Facades\Http;
use Illuminate\Support\Facades\Log;
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

    // Under PHP-FPM each request is a new process (a new Connector here):
    // a forged header must not add a log line to every request.
    public function test_an_ignored_value_is_logged_once_every_ten_minutes_across_processes(): void
    {
        Log::shouldReceive('warning')->once()
            ->with('Rhythm Echo: ignored an unsigned X-Echo-Run: with RHYTHM_ECHO_TOKEN set, only runs Echo signed with it count');
        Log::shouldReceive('warning')->once()
            ->with('Rhythm Echo: ignored an X-Echo-Run from 203.0.113.9: without RHYTHM_ECHO_TOKEN only a direct call from this machine may set the run (set the same token here and in Echo)');
        $client = $this->app->make(EchoClient::class);
        foreach ([1, 2, 3] as $process) {
            $this->assertSame('', (new Connector($this->app, $client, ['token' => 'secret']))->acceptRun('r1/checkout', '127.0.0.1', false), "process {$process}");
        }
        // another reason is still logged
        (new Connector($this->app, $client, []))->acceptRun('r1/checkout', '203.0.113.9', false);
        $this->assertTrue(Cache::has('rhythm-echo.runlog.unsigned'));
    }

    public function test_an_ignored_value_is_logged_with_a_broken_cache(): void
    {
        $this->app['config']->set('cache.default', 'missing');
        Log::shouldReceive('warning')->once();
        (new Connector($this->app, $this->app->make(EchoClient::class), ['token' => 'secret']))->acceptRun('r1/checkout', '127.0.0.1', false);
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
