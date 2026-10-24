<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

namespace Rhythm\Echo\Tests;

use Illuminate\Http\Client\ConnectionException;
use Illuminate\Support\Facades\Http;
use Rhythm\Echo\Tests\Fixtures\PingTwilio;

class HttpTest extends TestCase
{
    public function test_sealed_requests_get_echos_fake(): void
    {
        (new PingTwilio)->handle();
        $r = Http::asForm()->post('https://api.twilio.com/2010-04-01/Accounts/AC0/Messages.json', ['To' => '+15555550100']);

        $this->assertSame(201, $r->status());
        $this->assertSame('queued', $r->json('status'));
        $c = $this->captures('http');
        $this->assertCount(2, $c);
        $this->assertSame('before', $c[0]['phase']);
        $this->assertSame('POST', $c[0]['http']['method']);
        $this->assertStringContainsString('Body=Your+code+is+123456', $c[0]['http']['body']);
        $this->assertSame('•••••', $c[0]['http']['headers']['Authorization']);
        $this->assertSame('http', $c[0]['source']['hook']);
    }

    public function test_blocked_requests_fail_like_a_dead_connection(): void
    {
        $this->expectException(ConnectionException::class);
        $this->expectExceptionMessage('Rhythm Echo blocked');
        Http::get('https://blocked.test/hook');
    }

    public function test_allowed_requests_are_sent_and_reported_after(): void
    {
        $r = Http::post('http://127.0.0.1:'.self::PORT.'/target', ['hello' => 'world']);

        $this->assertTrue($r->json('pong'));
        $this->assertSame('{"hello":"world"}', $r->json('got'));
        $c = $this->captures('http');
        $this->assertCount(2, $c);
        $this->assertSame('after', $c[1]['phase']);
        $this->assertSame($c[0]['_id'], $c[1]['id']);
        $this->assertSame(200, $c[1]['http']['status']);
        $this->assertStringContainsString('"pong":true', $c[1]['http']['response']);
    }

    public function test_observe_sends_everything(): void
    {
        $this->mode('observe');
        $r = Http::get('http://127.0.0.1:'.self::PORT.'/target');

        $this->assertSame(200, $r->status());
        $this->assertSame(['before', 'after'], array_column($this->captures('http'), 'phase'));
    }

    public function test_calls_to_echo_itself_are_not_captured(): void
    {
        Http::get('http://127.0.0.1:'.self::PORT.'/v1/health');

        $this->assertSame([], $this->captures());
    }

    public function test_pool_requests_are_captured(): void
    {
        $responses = Http::pool(fn ($pool) => [
            $pool->post('https://api.twilio.com/one'),
            $pool->post('https://hooks.example.test/two'),
        ]);

        $this->assertSame(201, $responses[0]->status());
        $this->assertSame(200, $responses[1]->status());
        $this->assertCount(2, $this->captures('http'));
    }
}
