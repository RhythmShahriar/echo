<?php

namespace Rhythm\Echo\Tests\Fixtures;

use Illuminate\Bus\Queueable;
use Illuminate\Mail\Mailable;
use Illuminate\Mail\Mailables\Address;
use Illuminate\Mail\Mailables\Content;
use Illuminate\Mail\Mailables\Envelope;
use Illuminate\Queue\SerializesModels;

class OrderShipped extends Mailable
{
    use Queueable, SerializesModels;

    public function envelope(): Envelope
    {
        return new Envelope(
            subject: 'Your order #1001 has shipped',
            cc: ['ops@site.test'],
            bcc: ['audit@site.test'],
            replyTo: [new Address('help@site.test', 'Help')],
        );
    }

    public function content(): Content
    {
        /** @var view-string $text */
        $text = 'echo-test::text';

        return new Content(htmlString: '<p>Hi Ada, order #1001 is on its way.</p>', text: $text);
    }
}
