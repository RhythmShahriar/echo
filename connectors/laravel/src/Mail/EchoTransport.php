<?php

namespace Rhythm\Echo\Mail;

use Illuminate\Mail\Transport\ArrayTransport;
use Illuminate\Mail\Transport\LogTransport;
use Rhythm\Echo\Connector;
use Rhythm\Echo\EchoUnreachable;
use Rhythm\Echo\Wire;
use Symfony\Component\Mailer\Envelope;
use Symfony\Component\Mailer\Exception\TransportException;
use Symfony\Component\Mailer\SentMessage;
use Symfony\Component\Mailer\Transport\TransportInterface;
use Symfony\Component\Mime\RawMessage;

/**
 * Wraps a mailer's real transport. Every message is reported to Echo
 * first; Echo's decision says whether the real transport sends it.
 *
 * Sitting at the transport, under the Mailer, it sees the finished
 * message (after every MessageSending listener changed it) and can't be
 * skipped by another listener returning a value, which stops Laravel's
 * event dispatch before later listeners run.
 */
final class EchoTransport implements TransportInterface
{
    public function __construct(
        private readonly TransportInterface $inner,
        private readonly Connector $echo,
        private readonly string $mailer,
    ) {
    }

    public function inner(): TransportInterface
    {
        return $this->inner;
    }

    public function send(RawMessage $message, ?Envelope $envelope = null): ?SentMessage
    {
        if (! $this->echo->active()) {
            return $this->inner->send($message, $envelope);
        }
        try {
            $d = $this->echo->capture(['kind' => 'email', 'email' => EmailCapture::from($message)],
                ['plugin' => $this->mailer, 'hook' => 'mail.transport']);
        } catch (EchoUnreachable $e) {
            if (! $this->echo->sealed() || $this->staysLocal()) {
                return $this->inner->send($message, $envelope);
            }
            throw new TransportException($e->getMessage().'; sealed, so the mail was not sent', 0, $e);
        }

        return match ($d['action']) {
            Wire::ALLOW => $this->inner->send($message, $envelope),
            Wire::BLOCK => throw new TransportException('Rhythm Echo blocked this mail'),
            // Sealed: Echo kept it. The app sees a normal send.
            default => new SentMessage($message, $envelope ?? Envelope::create($message)),
        };
    }

    /** The array and log transports never leave the machine. */
    private function staysLocal(): bool
    {
        return $this->inner instanceof ArrayTransport || $this->inner instanceof LogTransport;
    }

    public function __toString(): string
    {
        return 'echo+'.$this->inner;
    }
}
