<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

namespace Rhythm\Echo\Tests;

use Illuminate\Support\Facades\Http;
use Illuminate\Support\Facades\Mail;
use Illuminate\Support\Facades\Route;
use Rhythm\Echo\Tests\Fixtures\OrderShipped;
use Rhythm\Echo\Wire;

/**
 * One Echo for many sites (an agency hub): each site names itself in
 * source.site and may hold only a write token, which can send captures but
 * not read them.
 */
class HubTest extends TestCase
{
    protected array $echoConfig = ['site_id' => 'client-a', 'write_token' => 'client-a-write'];

    protected function setUp(): void
    {
        parent::setUp();
        $this->requireToken('client-a-write');
    }

    protected function defineRoutes($router): void
    {
        Route::get('/contact', function () {
            Mail::to('ada@example.test')->send(new OrderShipped);
            Http::post('https://api.twilio.com/2010-04-01/Accounts/AC0/Messages.json');

            return 'sent';
        });
    }

    public function test_every_capture_names_the_site_and_carries_the_write_token(): void
    {
        $this->withServerVariables(['REMOTE_ADDR' => '203.0.113.9'])
            ->withHeader('X-Echo-Run', Wire::signRun('client-a-write', 'hub1/checkout'))->get('/contact')->assertOk();

        $c = $this->captures();
        $this->assertCount(2, $c);
        foreach ($c as $capture) {
            $this->assertSame('client-a', $capture['source']['site']);
            $this->assertSame('client-a-write', $capture['_headers']['token']);
            // Echo signs a hub site's runs with that site's token.
            $this->assertSame('hub1/checkout', $capture['run']);
        }
    }

    public function test_the_token_still_checks_runs_when_both_are_set(): void
    {
        $this->echoConfig['token'] = 'echo-token';
        $this->refreshApplication();
        $this->requireToken('client-a-write');
        $this->withServerVariables(['REMOTE_ADDR' => '203.0.113.9'])
            ->withHeader('X-Echo-Run', Wire::signRun('echo-token', 'hub2/checkout'))->get('/contact')->assertOk();

        $this->assertSame(['hub2/checkout', 'hub2/checkout'], array_column($this->captures(), 'run'));
        $this->assertSame(['client-a-write', 'client-a-write'], array_column(array_column($this->captures(), '_headers'), 'token'));
    }
}
