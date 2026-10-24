<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

namespace Rhythm\Echo\Tests;

use Illuminate\Support\Facades\Context;
use Illuminate\Support\Facades\Notification;
use Rhythm\Echo\Tests\Fixtures\OrderNote;
use Rhythm\Echo\Tests\Fixtures\QueuedOrderNote;

/**
 * Mail notifications go through the mailer; Vonage and Slack go over HTTP
 * through clients the connector covers.
 */
class NotificationTest extends TestCase
{
    public function test_mail_notifications_go_through_the_mailer(): void
    {
        Notification::route('mail', 'ada@example.test')->notify(new OrderNote(['mail']));

        $c = $this->captures('email');
        $this->assertCount(1, $c);
        $this->assertSame('Order #1001 received', $c[0]['email']['subject']);
        $this->assertSame(0, $this->delivered());
    }

    public function test_vonage_sms_is_captured_and_faked(): void
    {
        Notification::route('vonage', '15555550100')->notify(new OrderNote(['vonage']));

        $c = $this->captures('http');
        $this->assertCount(1, $c);
        $this->assertStringContainsString('nexmo.com/sms/json', $c[0]['http']['url']);
        $this->assertStringContainsString('Order', $c[0]['http']['body']);
        $this->assertSame('guzzle', $c[0]['source']['hook']);
        $this->assertStringStartsWith('vonage/', $c[0]['source']['plugin']);
    }

    public function test_slack_webhook_is_captured(): void
    {
        Notification::route('slack', 'https://hooks.slack.com/services/'.'TEST/TEST/test')->notify(new OrderNote(['slack']));

        $c = $this->captures('http');
        $this->assertCount(1, $c);
        $this->assertStringStartsWith('https://hooks.slack.com/', $c[0]['http']['url']);
        $this->assertSame('laravel/slack-notification-channel', $c[0]['source']['plugin']);
    }

    public function test_slack_web_api_is_captured(): void
    {
        Notification::route('slack', '#orders')->notify(new OrderNote(['slack'], slackApi: true));

        $c = $this->captures('http');
        $this->assertCount(1, $c);
        $this->assertSame('https://slack.com/api/chat.postMessage', $c[0]['http']['url']);
        $this->assertSame('Bearer •••••', $c[0]['http']['headers']['Authorization']);
        $this->assertSame('http', $c[0]['source']['hook']);
    }

    public function test_queued_notifications_carry_the_run(): void
    {
        $this->createJobsTable();
        Context::addHidden('rhythm_echo.run', 'r5/signup');
        Notification::route('mail', 'ada@example.test')->route('vonage', '15555550100')
            ->notify(new QueuedOrderNote(['mail', 'vonage']));
        Context::flush();

        $this->artisan('queue:work', ['--stop-when-empty' => true])->assertSuccessful();

        $c = $this->captures();
        $this->assertSame(['email', 'http'], array_column($c, 'kind'));
        $this->assertSame(['r5/signup', 'r5/signup'], array_column($c, 'run'));
    }
}
