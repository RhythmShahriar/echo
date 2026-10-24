<?php

namespace Rhythm\Echo\Tests;

use Illuminate\Support\Facades\Http;
use Illuminate\Support\Facades\Mail;
use Rhythm\Echo\Connector;
use Rhythm\Echo\Mail\EchoMailManager;

class GuardTest extends TestCase
{
    protected string $env = 'production';

    public function test_production_is_left_alone(): void
    {
        $this->assertFalse($this->app->make(Connector::class)->active());
        $this->assertNotInstanceOf(EchoMailManager::class, $this->app->make('mail.manager'));

        Mail::raw('Hi', fn ($m) => $m->to('a@example.test')->subject('Prod'));
        Http::get('http://127.0.0.1:'.self::PORT.'/target');

        $this->assertSame([], $this->captures());
        $this->assertSame(1, $this->delivered());
    }

    public function test_status_says_why(): void
    {
        $this->artisan('echo:status')->expectsOutputToContain('APP_ENV is production')->assertFailed();
    }
}
