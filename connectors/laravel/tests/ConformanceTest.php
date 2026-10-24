<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

namespace Rhythm\Echo\Tests;

use Illuminate\Http\Client\ConnectionException;
use Illuminate\Support\Facades\Cache;
use Illuminate\Support\Facades\Http;
use Illuminate\Support\Facades\Mail;
use PHPUnit\Framework\Attributes\DataProvider;
use ReflectionProperty;
use Rhythm\Echo\Connector;
use Rhythm\Echo\EchoClient;
use Rhythm\Echo\EchoUnreachable;
use Rhythm\Echo\Tests\Fixtures\WireSchema;
use Rhythm\Echo\Wire;

/**
 * Echo's shared conformance vectors (conformance/testdata in the Echo
 * repository; ECHO_CONFORMANCE names the folder when it lives elsewhere)
 * and the fallback to an Echo that speaks wire 1.
 */
class ConformanceTest extends TestCase
{
    /** @return array<string, array{0: array<string, mixed>}> */
    public static function vectors(): array
    {
        $dir = (getenv('ECHO_CONFORMANCE') ?: __DIR__.'/../../../conformance').'/testdata';
        $out = [];
        foreach (glob($dir.'/*.json') ?: [] as $file) {
            if (basename($file) !== 'run-signature.json') {
                $out[basename($file)] = [json_decode((string) file_get_contents($file), true, flags: JSON_THROW_ON_ERROR)];
            }
        }

        return $out ?: ['none' => [[]]];
    }

    /** @return array<string, array{0: array<string, mixed>}> */
    public static function failing(): array
    {
        return array_filter(self::vectors(), fn ($v) => ($v[0]['echo'] ?? 'up') !== 'up') ?: ['none' => [[]]];
    }

    /**
     * @param  array<string, mixed>  $v
     */
    #[DataProvider('vectors')]
    public function test_the_schema_agrees_with_echo(array $v): void
    {
        if ($v === [] || ! WireSchema::available()) {
            $this->markTestSkipped('no conformance vectors or wire schema here');
        }
        $problems = WireSchema::capture($v['capture']);
        $this->assertSame($v['valid'], $problems === [], implode("\n", $problems));
        foreach ($v['problems'] ?? [] as $want) {
            $this->assertStringContainsString($want, implode("\n", $problems));
        }
        if (isset($v['decision'])) {
            $this->assertSame([], WireSchema::decision(['id' => 'm1'] + $v['decision']));
        }
        if ($v['valid']) {
            // What the connector would send to an Echo that speaks wire 1.
            $this->assertSame([], WireSchema::capture(Wire::forVersion($v['capture'], 1)));
        }
    }

    /**
     * Every fail-closed vector: with no decision from Echo (nothing
     * listening, a 5xx, no answer in time) the connector blocks while
     * sealed and sends while observing.
     *
     * @param  array<string, mixed>  $v
     */
    #[DataProvider('failing')]
    public function test_no_decision(array $v): void
    {
        if ($v === []) {
            $this->markTestSkipped('no conformance vectors here');
        }
        $echo = $v['echo'];
        $this->echoConfig = ['mode' => $v['mode'], 'timeout' => 0.5] + ($echo === 'unreachable' ? ['url' => self::DEAD] : []);
        $this->refreshApplication();
        if ($echo !== 'unreachable') {
            $this->fail_echo($echo);
        }
        $target = 'http://127.0.0.1:'.self::PORT.'/target';
        if ($v['connector'] === 'block') {
            try {
                Http::get($target);
                $this->fail('the request went out');
            } catch (ConnectionException $e) {
                $this->assertStringContainsString('sealed, so the request was not sent', $e->getMessage());
            }
        } else {
            $this->assertTrue(Http::get($target)->json('pong'));
        }
    }

    public function test_an_echo_that_speaks_wire_1_gets_wire_1(): void
    {
        $this->wire(1);
        Mail::raw('one', fn ($m) => $m->to('a@example.test')->subject('One'));
        Mail::raw('two', fn ($m) => $m->to('a@example.test')->subject('Two'));

        $this->assertSame([1, 1], array_column($this->captures('email'), 'v'));
        $this->assertSame(1, $this->healthCalls());
        $this->assertSame(0, $this->delivered());
    }

    // Under PHP-FPM each request is a new process: the cache, not the
    // static, saves it a refused capture and a health call every time.
    public function test_a_new_process_remembers_the_wire_from_the_cache(): void
    {
        $this->wire(1);
        Mail::raw('one', fn ($m) => $m->to('a@example.test')->subject('One'));
        (new ReflectionProperty(EchoClient::class, 'wire'))->setValue(null, []);
        Mail::raw('two', fn ($m) => $m->to('a@example.test')->subject('Two'));

        $this->assertSame([1, 1], array_column($this->captures('email'), 'v'));
        $this->assertSame(1, $this->healthCalls());
        $this->assertSame(1, Cache::get('rhythm-echo.wire.'.md5('http://127.0.0.1:'.self::PORT))[0]);
    }

    public function test_a_broken_cache_still_falls_back(): void
    {
        $this->app['config']->set('cache.default', 'missing');
        $this->wire(1);
        Mail::raw('one', fn ($m) => $m->to('a@example.test')->subject('One'));
        Mail::raw('two', fn ($m) => $m->to('a@example.test')->subject('Two'));

        $this->assertSame([1, 1], array_column($this->captures('email'), 'v'));
        $this->assertSame(1, $this->healthCalls());
    }

    public function test_the_newest_wire_is_tried_again_after_an_hour(): void
    {
        $this->wire(1);
        Mail::raw('one', fn ($m) => $m->to('a@example.test')->subject('One'));
        // Echo is upgraded; within the hour the older version stays.
        $this->wire(2);
        Mail::raw('two', fn ($m) => $m->to('a@example.test')->subject('Two'));
        $this->assertSame([1, 1], array_column($this->captures('email'), 'v'));

        $wire = new ReflectionProperty(EchoClient::class, 'wire');
        $wire->setValue(null, array_map(fn (array $w) => [$w[0], $w[1] - EchoClient::WIRE_RECHECK], $wire->getValue()));
        // the cache entry expires with the hour too
        Cache::flush();
        Mail::raw('three', fn ($m) => $m->to('a@example.test')->subject('Three'));

        $this->assertSame([1, 1, 2], array_column($this->captures('email'), 'v'));
        $this->assertSame(Wire::VERSION, $this->app->make(Connector::class)->client()->wire());
    }

    public function test_wire_1_leaves_out_what_it_lacks(): void
    {
        $c = Wire::forVersion(['v' => 2, 'kind' => 'email', 'source' => ['via' => 'laravel', 'site' => 'a'], 'email' => ['attachments' => [['name' => 'a.pdf']]]], 1);

        $this->assertSame(['v' => 1, 'kind' => 'email', 'source' => ['via' => 'laravel'], 'email' => []], $c);
    }

    public function test_a_broken_capture_is_refused(): void
    {
        $this->allowInvalid = true;
        $client = $this->app->make(Connector::class)->client();
        $this->expectException(EchoUnreachable::class);
        $this->expectExceptionMessage('conformance: capture.kind: "sms" is not one of email, http');
        $client->capture(['v' => 2, 'kind' => 'sms', 'source' => ['via' => 'laravel']]);
    }
}
