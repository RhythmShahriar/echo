<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

namespace Rhythm\Echo\Tests;

class CommandTest extends TestCase
{
    public function test_status(): void
    {
        $this->artisan('echo:status')
            ->expectsOutputToContain('active:    yes')
            ->expectsOutputToContain('reachable, mode seal')
            ->assertSuccessful();
    }

    public function test_status_json(): void
    {
        $this->mode('observe');
        $this->artisan('echo:status', ['--json' => true])->expectsOutputToContain('"mode": "observe"')->assertSuccessful();
    }

    public function test_canary_goes_through_the_normal_paths(): void
    {
        putenv('RHYTHM_ECHO_RUN=doctor/canary');
        $this->artisan('echo:canary')->expectsOutput('mail: ok')->expectsOutput('http: ok, 200')->assertSuccessful();

        $c = $this->captures();
        $this->assertSame(['email', 'http'], array_column($c, 'kind'));
        $this->assertSame(['canary@echo.invalid'], $c[0]['email']['to']);
        $this->assertSame('https://canary.echo.invalid/', $c[1]['http']['url']);
        $this->assertSame(['doctor/canary', 'doctor/canary'], array_column($c, 'run'));
    }
}
