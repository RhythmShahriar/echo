<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

namespace Rhythm\Echo\Mail;

use Rhythm\Echo\Connector;
use Symfony\Component\Mime\Address;
use Symfony\Component\Mime\Email;
use Symfony\Component\Mime\RawMessage;
use Throwable;

/**
 * Turns a Symfony message into the email part of a capture.
 */
final class EmailCapture
{
    /** Full MIME above this size is left out; the parsed fields still go. */
    private const MAX_RAW = 2 << 20;

    /** Headers already sent as fields. */
    private const FIELD_HEADERS = ['from', 'to', 'cc', 'bcc', 'reply-to', 'subject'];

    /**
     * @return array<string, mixed>
     */
    public static function from(RawMessage $message): array
    {
        if ($message instanceof Email && ! $message->getHeaders()->has('Message-ID') && $message->getFrom() !== []) {
            // Fixed now, so the capture and the delivered mail share it.
            $message->getHeaders()->addIdHeader('Message-ID', $message->generateMessageId());
        }
        try {
            $raw = $message->toString();
        } catch (Throwable) {
            $raw = '';
        }
        $email = array_filter([
            'raw' => strlen($raw) <= self::MAX_RAW ? $raw : '',
        ]);
        if (! $message instanceof Email) {
            return $email; // Echo parses the MIME
        }
        try {
            $all = $message->getPreparedHeaders(); // with Date and Message-ID, as sent
        } catch (Throwable) {
            $all = $message->getHeaders(); // not valid yet (no sender): as is
        }
        $headers = [];
        foreach ($all->all() as $header) {
            $name = $header->getName();
            if (! in_array(strtolower($name), self::FIELD_HEADERS, true)) {
                $headers[$name] = $header->getBodyAsString();
            }
        }
        $replyTo = $message->getReplyTo();

        return array_filter([
            'from' => self::list($message->getFrom())[0] ?? '',
            'to' => self::list($message->getTo()),
            'cc' => self::list($message->getCc()),
            'bcc' => self::list($message->getBcc()),
            'reply_to' => $replyTo === [] ? '' : $replyTo[0]->toString(),
            'subject' => (string) $message->getSubject(),
            'text' => self::body($message->getTextBody()),
            'html' => self::body($message->getHtmlBody()),
            'headers' => Connector::headers($headers),
        ], fn ($v) => $v !== '' && $v !== []) + $email;
    }

    /**
     * @param  Address[]  $addresses
     * @return string[]
     */
    private static function list(array $addresses): array
    {
        return array_map(fn (Address $a) => $a->toString(), $addresses);
    }

    /**
     * @param  resource|string|null  $body
     */
    private static function body(mixed $body): string
    {
        if (is_resource($body)) {
            $s = stream_get_contents($body);
            @rewind($body);

            return $s === false ? '' : $s;
        }

        return (string) $body;
    }
}
