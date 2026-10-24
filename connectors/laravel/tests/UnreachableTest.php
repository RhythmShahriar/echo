<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

namespace Rhythm\Echo\Tests;

use Illuminate\Http\Client\ConnectionException;
use Illuminate\Support\Facades\Http;
use Illuminate\Support\Facades\Mail;
use Symfony\Component\Mailer\Exception\TransportException;

/** Echo is down and the connector is sealed: nothing leaves. */
class UnreachableTest extends TestCase
{
    protected array $echoConfig = ['url' => self::DEAD, 'timeout' => 0.5];

    public function test_mail_fails_closed(): void
    {
        $this->expectException(TransportException::class);
        $this->expectExceptionMessage('sealed, so the mail was not sent');
        Mail::mailer('smtp')->raw('Hi', fn ($m) => $m->to('a@example.test')->subject('Down'));
    }

    public function test_local_mailers_still_work(): void
    {
        Mail::raw('Hi', fn ($m) => $m->to('a@example.test')->subject('Down'));

        $this->assertSame(1, $this->delivered());
    }

    public function test_http_fails_closed(): void
    {
        try {
            Http::get('http://127.0.0.1:'.self::PORT.'/target');
            $this->fail('the request went out');
        } catch (ConnectionException $e) {
            $this->assertStringContainsString('sealed, so the request was not sent', $e->getMessage());
        }
    }
}
