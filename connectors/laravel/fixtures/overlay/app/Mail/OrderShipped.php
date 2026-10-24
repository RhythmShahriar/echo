<?php

namespace App\Mail;

use Illuminate\Mail\Mailable;
use Illuminate\Mail\Mailables\Content;
use Illuminate\Mail\Mailables\Envelope;

class OrderShipped extends Mailable
{
    public function envelope(): Envelope
    {
        return new Envelope(subject: 'Your order #1001 has shipped');
    }

    public function content(): Content
    {
        return new Content(htmlString: '<p>Hi Ada, order #1001 is on its way.</p>');
    }
}
