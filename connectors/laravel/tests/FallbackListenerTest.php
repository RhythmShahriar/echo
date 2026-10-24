<?php

namespace Rhythm\Echo\Tests;

use Illuminate\Mail\MailManager;
use Illuminate\Support\Facades\Mail;
use Rhythm\Echo\Mail\EchoMailManager;

class FallbackListenerTest extends TestCase
{
    protected function defineEnvironment($app): void
    {
        parent::defineEnvironment($app);
        $app->loadDeferredProvider('mail.manager');
        $app->singleton('mail.manager', fn ($app) => new class($app) extends MailManager {
        });
    }

    public function test_listener_seals_unwrapped_mailers(): void
    {
        $this->assertNotInstanceOf(EchoMailManager::class, $this->app->make('mail.manager'));
        Mail::raw('Hi', fn ($m) => $m->to('a@example.test')->subject('Fallback'));

        $c = $this->captures('email');
        $this->assertCount(1, $c);
        $this->assertSame('MessageSending', $c[0]['source']['hook']);
        $this->assertSame('array', $c[0]['source']['plugin']);
        $this->assertSame(0, $this->delivered());
    }

    public function test_listener_lets_observed_mail_through(): void
    {
        $this->mode('observe');
        Mail::raw('Hi', fn ($m) => $m->to('a@example.test')->subject('Fallback'));

        $this->assertSame(1, $this->delivered());
    }
}
