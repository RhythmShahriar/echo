<?php

namespace Rhythm\Echo\Tests;

use Rhythm\Echo\Connector;

/** With nothing configured, the app's own tests are left alone. */
class DefaultsTest extends TestCase
{
    protected array $echoConfig = ['enabled' => null];

    public function test_testing_environment_is_off_by_default(): void
    {
        $echo = $this->app->make(Connector::class);
        $this->assertFalse($echo->active());
        $this->assertStringContainsString('testing', (string) $echo->inactiveReason());
    }
}
