<?php

namespace Rhythm\Echo\Tests;

use Rhythm\Echo\Connector;
use Rhythm\Echo\EchoClient;

class StagingDefaultsTest extends TestCase
{
    protected string $env = 'staging';

    protected array $echoConfig = ['enabled' => null];

    public function test_staging_is_on_by_default(): void
    {
        $this->assertTrue($this->app->make(Connector::class)->active());
    }

    public function test_an_empty_setting_means_unset(): void
    {
        $echo = new Connector($this->app, $this->app->make(EchoClient::class), ['enabled' => '']);
        $this->assertTrue($echo->active());

        $off = new Connector($this->app, $this->app->make(EchoClient::class), ['enabled' => 'false']);
        $this->assertFalse($off->active());
    }
}
