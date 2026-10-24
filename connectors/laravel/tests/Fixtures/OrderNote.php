<?php

namespace Rhythm\Echo\Tests\Fixtures;

use Illuminate\Bus\Queueable;
use Illuminate\Contracts\Queue\ShouldQueue;
use Illuminate\Notifications\Messages\MailMessage;
use Illuminate\Notifications\Messages\SlackMessage as WebhookMessage;
use Illuminate\Notifications\Messages\VonageMessage;
use Illuminate\Notifications\Notification;
use Illuminate\Notifications\Slack\SlackMessage;

/** Mail, Vonage SMS, Slack webhook and Slack Web API, by route. */
class OrderNote extends Notification
{
    use Queueable;

    /** @param list<string> $via */
    public function __construct(public array $via = ['mail'], public bool $slackApi = false)
    {
    }

    /** @return list<string> */
    public function via(object $notifiable): array
    {
        return $this->via;
    }

    public function toMail(object $notifiable): MailMessage
    {
        return (new MailMessage)->subject('Order #1001 received')->line('Thanks for your order.');
    }

    public function toVonage(object $notifiable): VonageMessage
    {
        return (new VonageMessage)->content('Order #1001 received');
    }

    public function toSlack(object $notifiable): WebhookMessage|SlackMessage
    {
        if ($this->slackApi) {
            return (new SlackMessage)->to('#orders')->text('Order #1001 received');
        }

        return (new WebhookMessage)->content('Order #1001 received');
    }
}
