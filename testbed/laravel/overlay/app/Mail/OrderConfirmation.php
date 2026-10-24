<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

namespace App\Mail;

use Illuminate\Bus\Queueable;
use Illuminate\Contracts\Queue\ShouldQueue;
use Illuminate\Mail\Mailable;
use Illuminate\Mail\Mailables\Content;
use Illuminate\Mail\Mailables\Envelope;
use Illuminate\Queue\SerializesModels;

// Queued: the queue worker (the flush, `php artisan queue:work`) renders and
// sends it.
//
// Broken state (testbed/scenarios.yaml):
// - s06: a refactor renamed the view to emails.order-confirmation but the
//   template was never moved. The job throws in the worker ("View not
//   found") and the email is never sent.
class OrderConfirmation extends Mailable implements ShouldQueue
{
    use Queueable, SerializesModels;

    public function __construct(public int $order) {}

    public function envelope(): Envelope
    {
        return new Envelope(subject: "Your subscription box #{$this->order} is confirmed");
    }

    public function content(): Content
    {
        $view = file_exists('/state/s06') ? 'emails.order-confirmation' : 'mail.order-confirmation';

        return new Content(text: $view);
    }
}
