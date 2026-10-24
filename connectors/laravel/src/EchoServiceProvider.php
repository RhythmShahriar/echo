<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

namespace Rhythm\Echo;

use GuzzleHttp\Client as GuzzleClient;
use GuzzleHttp\HandlerStack;
use Illuminate\Contracts\Http\Kernel as HttpKernel;
use Illuminate\Foundation\Http\Kernel;
use Illuminate\Http\Client\Factory as HttpFactory;
use Illuminate\Mail\Events\MessageSending;
use Illuminate\Mail\MailManager;
use Illuminate\Support\Facades\Event;
use Illuminate\Support\ServiceProvider;
use Rhythm\Echo\Console\CanaryCommand;
use Rhythm\Echo\Console\StatusCommand;
use Rhythm\Echo\Http\CaptureRequests;
use Rhythm\Echo\Http\TrackRun;
use Rhythm\Echo\Mail\EchoMailManager;
use Rhythm\Echo\Mail\SealOnSending;

/**
 * Wires the connector into mail, the Http client, container-built Guzzle
 * clients and incoming requests. When it is inactive (production, tests,
 * or turned off) only the commands are registered, so `echo:status` can
 * say why.
 */
class EchoServiceProvider extends ServiceProvider
{
    public function register(): void
    {
        $this->mergeConfigFrom(__DIR__.'/../config/rhythm-echo.php', 'rhythm-echo');

        $this->app->singleton(EchoClient::class, function ($app) {
            $c = $app['config']['rhythm-echo'];

            // A write token, when set, is what goes to Echo: it can send
            // captures and nothing else.
            $token = ($c['write_token'] ?? '') ?: ($c['token'] ?? null);

            return new EchoClient(
                (string) $c['url'], $token, (float) ($c['timeout'] ?? 2.0),
                cache: fn () => $app->bound('cache') ? $app->make('cache')->store() : null,
            );
        });
        $this->app->singleton(Connector::class, fn ($app) => new Connector($app, $app->make(EchoClient::class), $app['config']['rhythm-echo']));

        // Whether the connector is active is decided when these resolve,
        // not here: config and the environment may still change before boot.
        $this->app->extend('mail.manager', function ($manager, $app) {
            $echo = $app->make(Connector::class);
            // An app that binds its own manager keeps it; the MessageSending
            // listener covers it instead.
            return $echo->active() && $manager::class === MailManager::class
                ? EchoMailManager::wrap($manager, $echo)
                : $manager;
        });

        // Packages that type-hint GuzzleHttp\Client get it from the
        // container: give them one that reports to Echo.
        $this->app->bind(GuzzleClient::class, function ($app) {
            $echo = $app->make(Connector::class);
            if (! $echo->active() || ! $app['config']->get('rhythm-echo.guzzle', true)) {
                return new GuzzleClient;
            }
            $stack = HandlerStack::create();
            $stack->push(new CaptureRequests($echo, 'guzzle'), 'rhythm-echo');

            return new GuzzleClient(['handler' => $stack]);
        });
    }

    public function boot(): void
    {
        $this->publishes([__DIR__.'/../config/rhythm-echo.php' => $this->app->configPath('rhythm-echo.php')], 'rhythm-echo');
        if ($this->app->runningInConsole()) {
            $this->commands([StatusCommand::class, CanaryCommand::class]);
        }

        $echo = $this->app->make(Connector::class);
        if (! $echo->active()) {
            return;
        }

        $this->app->make(HttpFactory::class)->globalMiddleware(new CaptureRequests($echo, 'http'));

        $config = $this->app['config'];
        if ($config->get('rhythm-echo.guzzle', true) && $config->get('vonage.http_client') === null) {
            // The Vonage channel builds its own Guzzle client unless told
            // which PSR-18 client to use.
            $config->set('vonage.http_client', GuzzleClient::class);
        }

        Event::listen(MessageSending::class, function (MessageSending $event) use ($echo) {
            if ($this->app->make('mail.manager') instanceof EchoMailManager) {
                return null; // the transport reports it
            }

            return (new SealOnSending($echo))->handle($event);
        });

        if ($this->app->bound(HttpKernel::class)) {
            /** @var HttpKernel $kernel the app's kernel may not extend Laravel's */
            $kernel = $this->app->make(HttpKernel::class);
            if ($kernel instanceof Kernel) {
                $kernel->prependMiddleware(TrackRun::class);
            }
        }
    }
}
