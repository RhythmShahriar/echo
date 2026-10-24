<?php

namespace Rhythm\Echo\Http;

use Closure;
use Illuminate\Http\Request;
use Rhythm\Echo\Connector;
use Rhythm\Echo\Wire;
use Symfony\Component\HttpFoundation\Response;

/**
 * Global middleware: copies X-Echo-Run (header first, then cookie) into a
 * hidden Context value. Laravel serialises Context into every job queued
 * during the request and restores it in the worker, so queued mail and
 * jobs keep the run without the connector touching the queue.
 *
 * It runs before EncryptCookies, so the cookie is still the raw value
 * Echo's browser set.
 */
final class TrackRun
{
    public function __construct(private readonly Connector $echo)
    {
    }

    public function handle(Request $request, Closure $next): Response
    {
        if ($this->echo->active()) {
            $run = Connector::cleanRun($request->headers->get(Wire::RUN_HEADER));
            if ($run === '') {
                $run = Connector::cleanRun($request->cookies->get(Wire::RUN_HEADER));
            }
            if ($run !== '') {
                $this->echo->remember($run);
            }
        }

        return $next($request);
    }
}
