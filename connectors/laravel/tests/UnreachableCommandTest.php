<?php

namespace Rhythm\Echo\Tests;

class UnreachableCommandTest extends TestCase
{
    protected array $echoConfig = ['url' => self::DEAD, 'timeout' => 0.5];

    public function test_status_fails_when_echo_is_down(): void
    {
        $this->artisan('echo:status')->expectsOutputToContain('unreachable')->assertFailed();
    }
}
