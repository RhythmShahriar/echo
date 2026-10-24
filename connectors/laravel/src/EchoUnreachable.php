<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

namespace Rhythm\Echo;

use RuntimeException;

/** Echo didn't answer, or answered with something that isn't a decision. */
final class EchoUnreachable extends RuntimeException
{
    /** Echo's HTTP status, 0 when it did not answer at all. */
    public int $status = 0;

    /**
     * Why a sealed connector refused to send: this error, that nothing
     * left and how to find out why Echo can't be reached. $what is "mail"
     * or "request".
     */
    public function sealedMessage(string $what): string
    {
        return $this->getMessage()."; sealed, so the {$what} was not sent. "
            .'Check the site reaches Echo: php artisan echo:status, or rhythm echo doctor';
    }
}
