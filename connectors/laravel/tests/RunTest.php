<?php

namespace Rhythm\Echo\Tests;

use Illuminate\Support\Facades\Context;
use Illuminate\Support\Facades\Http;
use Illuminate\Support\Facades\Mail;
use Illuminate\Support\Facades\Route;
use Rhythm\Echo\Connector;
use Rhythm\Echo\Tests\Fixtures\OrderShipped;
use Rhythm\Echo\Tests\Fixtures\PingTwilio;

/** X-Echo-Run: header, then cookie, then job Context, then RHYTHM_ECHO_RUN. */
class RunTest extends TestCase
{
    protected function defineRoutes($router): void
    {
        Route::get('/contact', function () {
            Mail::to('ada@example.test')->send(new OrderShipped);
            Http::post('https://api.twilio.com/2010-04-01/Accounts/AC0/Messages.json');

            return 'sent';
        });
        Route::get('/queue', function () {
            Mail::to('ada@example.test')->queue(new OrderShipped);
            PingTwilio::dispatch();

            return 'queued';
        });
    }

    /** @return list<string> */
    private function runs(): array
    {
        return array_map(fn ($c) => $c['run'] ?? '', $this->captures());
    }

    public function test_header(): void
    {
        $this->withHeader('X-Echo-Run', 'r1/contact')->get('/contact')->assertOk();

        $this->assertSame(['r1/contact', 'r1/contact'], $this->runs());
    }

    public function test_cookie(): void
    {
        $this->withUnencryptedCookie('X-Echo-Run', 'r2/contact')->get('/contact')->assertOk();

        $this->assertSame(['r2/contact', 'r2/contact'], $this->runs());
    }

    public function test_header_beats_cookie(): void
    {
        $this->withHeader('X-Echo-Run', 'r1/header')->withUnencryptedCookie('X-Echo-Run', 'r1/cookie')->get('/contact');

        $this->assertSame(['r1/header', 'r1/header'], $this->runs());
    }

    public function test_the_run_stays_out_of_logged_context(): void
    {
        $this->withHeader('X-Echo-Run', 'r1/contact')->get('/contact');

        $this->assertNull(Context::get(Connector::CONTEXT_KEY));
        $this->assertSame('r1/contact', Context::getHidden(Connector::CONTEXT_KEY));
    }

    public function test_environment_when_there_is_no_request(): void
    {
        putenv('RHYTHM_ECHO_RUN=r3/cli');
        Mail::raw('Hi', fn ($m) => $m->to('a@example.test')->subject('CLI'));

        $this->assertSame(['r3/cli'], $this->runs());
    }

    public function test_request_beats_environment(): void
    {
        putenv('RHYTHM_ECHO_RUN=r3/cli');
        $this->withHeader('X-Echo-Run', 'r1/contact')->get('/contact');

        $this->assertSame(['r1/contact', 'r1/contact'], $this->runs());
    }

    public function test_no_run(): void
    {
        $this->get('/contact')->assertOk();

        $this->assertSame(['', ''], $this->runs());
    }

    public function test_garbage_is_ignored(): void
    {
        $this->withHeader('X-Echo-Run', "bad\x01run")->get('/contact');

        $this->assertSame(['', ''], $this->runs());
    }

    public function test_queued_mail_and_jobs_carry_the_run(): void
    {
        $this->createJobsTable();
        $this->withHeader('X-Echo-Run', 'r4/checkout')->get('/queue')->assertOk();
        $this->assertSame([], $this->captures(), 'nothing is sent until the worker runs');

        // The worker is another process: no request, an empty Context,
        // and Echo's runner set the env for the flush command.
        Context::flush();
        putenv('RHYTHM_ECHO_RUN=r9/flush');
        $this->artisan('queue:work', ['--stop-when-empty' => true])->assertSuccessful();

        $this->assertSame(['r4/checkout', 'r4/checkout'], $this->runs());
        $this->assertSame(['email', 'http'], array_column($this->captures(), 'kind'));
    }

    public function test_jobs_queued_without_a_run_fall_back_to_the_environment(): void
    {
        $this->createJobsTable();
        $this->get('/queue')->assertOk();

        Context::flush();
        putenv('RHYTHM_ECHO_RUN=r9/flush');
        $this->artisan('queue:work', ['--stop-when-empty' => true])->assertSuccessful();

        $this->assertSame(['r9/flush', 'r9/flush'], $this->runs());
    }
}
