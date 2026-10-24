<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

namespace Rhythm\Echo\Tests;

use Illuminate\Mail\Events\MessageSending;
use Illuminate\Mail\Events\MessageSent;
use Illuminate\Support\Facades\Event;
use Illuminate\Support\Facades\Mail;

/**
 * Spike, kept as a regression test: how MessageSending cancellation works
 * in the installed Laravel and why the transport is the primary path.
 */
class MessageSendingTest extends TestCase
{
    protected array $echoConfig = ['enabled' => false];

    public function test_a_listener_returning_false_cancels_the_send(): void
    {
        $sent = 0;
        Event::listen(MessageSending::class, fn () => false);
        Event::listen(MessageSent::class, function () use (&$sent) {
            $sent++;
        });

        $result = Mail::raw('Hi', fn ($m) => $m->to('a@example.test')->subject('Cancel'));

        $this->assertNull($result);
        $this->assertSame(0, $sent);
        $this->assertSame(0, $this->delivered());
    }

    public function test_an_earlier_listener_returning_a_value_skips_later_ones(): void
    {
        $ran = false;
        Event::listen(MessageSending::class, fn () => true);
        Event::listen(MessageSending::class, function () use (&$ran) {
            $ran = true;

            return false;
        });

        Mail::raw('Hi', fn ($m) => $m->to('a@example.test')->subject('Halt'));

        $this->assertFalse($ran, 'Laravel halts on the first non-null listener result');
        $this->assertSame(1, $this->delivered());
    }
}

