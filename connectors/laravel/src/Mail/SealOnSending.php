<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

namespace Rhythm\Echo\Mail;

use Illuminate\Mail\Events\MessageSending;
use Rhythm\Echo\Connector;
use Rhythm\Echo\EchoUnreachable;
use Rhythm\Echo\Wire;

/**
 * Fallback for apps whose mail manager couldn't be wrapped (they bind
 * their own): report each message from MessageSending and return false
 * to cancel it when Echo keeps it. Laravel stops sending when a
 * MessageSending listener returns false but an earlier listener that
 * returns anything non-null ends the dispatch first, so the transport
 * wrapper is the primary path.
 */
final class SealOnSending
{
    public function __construct(private readonly Connector $echo)
    {
    }

    /**
     * Returns false to cancel the send when Echo keeps the message or,
     * sealed, can't be reached; null lets it go on.
     */
    public function handle(MessageSending $event): ?bool
    {
        if (! $this->echo->active()) {
            return null;
        }
        try {
            $d = $this->echo->capture(['kind' => 'email', 'email' => EmailCapture::from($event->message)],
                ['plugin' => (string) ($event->data['mailer'] ?? ''), 'hook' => 'MessageSending']);
        } catch (EchoUnreachable $e) {
            report($e);

            return $this->echo->sealed() ? false : null;
        }

        return $d['action'] === Wire::ALLOW ? null : false;
    }
}
