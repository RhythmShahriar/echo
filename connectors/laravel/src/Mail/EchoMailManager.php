<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

namespace Rhythm\Echo\Mail;

use Illuminate\Mail\MailManager;
use Rhythm\Echo\Connector;

/**
 * Laravel's MailManager with every mailer it builds (configured, on-demand
 * via Mail::build, failover and round-robin alike) wrapped in an
 * EchoTransport. Children of failover and round-robin aren't wrapped
 * again: one message, one capture.
 */
class EchoMailManager extends MailManager
{
    private Connector $echo;

    /**
     * Replaces $manager, keeping transports registered with Mail::extend.
     */
    public static function wrap(MailManager $manager, Connector $echo): self
    {
        $new = new self($manager->app);
        $new->customCreators = $manager->customCreators;
        $new->echo = $echo;

        return $new;
    }

    /**
     * @param  array<string, mixed>  $config
     */
    public function build($config)
    {
        $mailer = parent::build($config);
        if (! $mailer->getSymfonyTransport() instanceof EchoTransport) {
            $mailer->setSymfonyTransport(new EchoTransport($mailer->getSymfonyTransport(), $this->echo, (string) ($config['name'] ?? 'ondemand')));
        }

        return $mailer;
    }
}
