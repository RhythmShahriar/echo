<?php

namespace Rhythm\Echo\Tests;

use Illuminate\Support\Facades\Mail;
use Rhythm\Echo\Connector;

class ForcedTest extends TestCase
{
    protected string $env = 'production';

    protected array $echoConfig = ['force' => true];

    public function test_force_runs_in_production(): void
    {
        $this->assertTrue($this->app->make(Connector::class)->active());
        Mail::raw('Hi', fn ($m) => $m->to('a@example.test')->subject('Forced'));

        $this->assertCount(1, $this->captures('email'));
        $this->assertSame(0, $this->delivered());
    }
}
