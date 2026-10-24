<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

namespace Rhythm\Echo\Tests;

use Illuminate\Support\Facades\Http;

class UnreachableObserveTest extends TestCase
{
    protected array $echoConfig = ['url' => self::DEAD, 'timeout' => 0.5, 'mode' => 'observe'];

    public function test_observe_lets_requests_through(): void
    {
        $this->assertTrue(Http::get('http://127.0.0.1:'.self::PORT.'/target')->json('pong'));
    }
}
