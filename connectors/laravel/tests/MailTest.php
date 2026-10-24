<?php

namespace Rhythm\Echo\Tests;

use Illuminate\Support\Facades\Mail;
use Rhythm\Echo\Mail\EchoMailManager;
use Rhythm\Echo\Mail\EchoTransport;
use Rhythm\Echo\Tests\Fixtures\OrderShipped;

class MailTest extends TestCase
{
    public function test_every_mailer_is_wrapped(): void
    {
        $this->assertInstanceOf(EchoMailManager::class, $this->app->make('mail.manager'));
        $this->assertInstanceOf(EchoTransport::class, Mail::mailer('array')->getSymfonyTransport());
        $this->assertInstanceOf(EchoTransport::class, Mail::mailer('smtp')->getSymfonyTransport());
        $this->assertInstanceOf(EchoTransport::class, Mail::build(['transport' => 'array'])->getSymfonyTransport());
    }

    public function test_sealed_mail_is_captured_and_not_sent(): void
    {
        Mail::to([['email' => 'ada@example.test', 'name' => 'Ada']])->send(new OrderShipped);

        $this->assertSame(0, $this->delivered());
        $c = $this->captures('email');
        $this->assertCount(1, $c);
        $e = $c[0]['email'];
        $this->assertSame(1, $c[0]['v']);
        $this->assertSame('"Site" <shop@site.test>', $e['from']);
        $this->assertSame(['"Ada" <ada@example.test>'], $e['to']);
        $this->assertSame(['ops@site.test'], $e['cc']);
        $this->assertSame(['audit@site.test'], $e['bcc']);
        $this->assertSame('"Help" <help@site.test>', $e['reply_to']);
        $this->assertSame('Your order #1001 has shipped', $e['subject']);
        $this->assertStringContainsString('order #1001 is on its way', $e['text']);
        $this->assertStringContainsString('<p>Hi Ada', $e['html']);
        $this->assertArrayHasKey('Message-ID', $e['headers']);
        $this->assertStringContainsString('Subject: Your order #1001', $e['raw']);
        $this->assertSame(['via' => 'laravel', 'stack' => 'laravel', 'plugin' => 'array', 'hook' => 'mail.transport', 'host' => 'site.test'], $c[0]['source']);
    }

    public function test_the_mailer_name_is_the_plugin(): void
    {
        $this->app['config']->set('mail.mailers.marketing', ['transport' => 'array']);
        Mail::mailer('marketing')->raw('Hi', fn ($m) => $m->to('a@example.test')->subject('Promo'));

        $this->assertSame('marketing', $this->captures('email')[0]['source']['plugin']);
    }

    public function test_observed_mail_is_captured_and_sent(): void
    {
        $this->mode('observe');
        Mail::to('ada@example.test')->send(new OrderShipped);

        $this->assertSame(1, $this->delivered());
        $this->assertCount(1, $this->captures('email'));
    }

    public function test_failover_mailer_captures_once(): void
    {
        $this->app['config']->set('mail.mailers.failover', ['transport' => 'failover', 'mailers' => ['array', 'log']]);
        Mail::mailer('failover')->raw('Hi', fn ($m) => $m->to('a@example.test')->subject('Failover'));

        $c = $this->captures('email');
        $this->assertCount(1, $c);
        $this->assertSame('failover', $c[0]['source']['plugin']);
    }

    public function test_credentials_in_mail_headers_are_masked(): void
    {
        Mail::raw('Hi', fn ($m) => $m->to('a@example.test')->subject('Hdr')->getHeaders()->addTextHeader('X-Api-Key', 'abc123secret'));

        $this->assertSame('•••••', $this->captures('email')[0]['email']['headers']['X-Api-Key']);
    }

    public function test_the_token_is_sent(): void
    {
        $this->app['config']->set('rhythm-echo.token', 'tok-123');
        $this->app->forgetInstance(\Rhythm\Echo\EchoClient::class);
        $this->app->forgetInstance(\Rhythm\Echo\Connector::class);
        $this->app->forgetInstance('mail.manager');
        Mail::clearResolvedInstances();
        $this->requireToken('tok-123');

        Mail::raw('Hi', fn ($m) => $m->to('a@example.test')->subject('Token'));

        $this->assertSame('tok-123', $this->captures('email')[0]['_headers']['token']);
        $this->assertSame(0, $this->delivered());
    }
}
