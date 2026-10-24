<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

namespace Rhythm\Echo\Tests;

use Illuminate\Support\Facades\Http;
use Illuminate\Support\Facades\Log;
use Illuminate\Support\Facades\Mail;
use Illuminate\Support\Facades\Route;
use Rhythm\Echo\Tests\Fixtures\OrderShipped;

/** X-Echo-Run with a token set: only signed values count, from anywhere. */
class SignedRunTest extends TestCase
{
    protected array $echoConfig = ['token' => 'secret'];

    protected function setUp(): void
    {
        parent::setUp();
        $this->requireToken('secret');
    }

    protected function defineRoutes($router): void
    {
        Route::get('/contact', function () {
            Mail::to('ada@example.test')->send(new OrderShipped);
            Http::post('https://api.twilio.com/2010-04-01/Accounts/AC0/Messages.json');

            return 'sent';
        });
    }

    /** @return list<string> */
    private function runs(): array
    {
        return array_map(fn ($c) => $c['run'] ?? '', $this->captures());
    }

    public function test_a_signed_header_sets_the_run(): void
    {
        $this->withServerVariables(['REMOTE_ADDR' => '203.0.113.9'])
            ->withHeader('X-Echo-Run', RunSignatureTest::SIGNED)->get('/contact')->assertOk();

        $this->assertSame(['r1/checkout', 'r1/checkout'], $this->runs());
    }

    public function test_a_signed_cookie_sets_the_run(): void
    {
        $this->withUnencryptedCookie('X-Echo-Run', RunSignatureTest::SIGNED)->get('/contact')->assertOk();

        $this->assertSame(['r1/checkout', 'r1/checkout'], $this->runs());
    }

    public function test_a_tampered_value_is_ignored_and_logged_once(): void
    {
        Log::shouldReceive('warning')->once()
            ->with('Rhythm Echo: ignored an X-Echo-Run whose signature does not verify (not signed with this token)');
        $this->withHeader('X-Echo-Run', 'r1/refund.NhOp3HY0JQ76wSoPnn3sZg')->get('/contact')->assertOk();
        $this->withHeader('X-Echo-Run', 'r1/refund.NhOp3HY0JQ76wSoPnn3sZg')->get('/contact')->assertOk();

        $this->assertSame(['', '', '', ''], $this->runs());
    }

    public function test_an_unsigned_value_is_ignored_when_a_token_is_set(): void
    {
        $this->withHeader('X-Echo-Run', 'r1/checkout')->get('/contact')->assertOk();

        $this->assertSame(['', ''], $this->runs());
    }
}
