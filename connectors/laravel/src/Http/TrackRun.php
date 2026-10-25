<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

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
 * Echo's browser set. The value counts only as Connector::acceptRun()
 * says: signed with the token, or sent straight from this machine.
 */
final class TrackRun
{
    public function __construct(private readonly Connector $echo)
    {
    }

    /** Remembers the request's accepted run, if any, then passes it on. */
    public function handle(Request $request, Closure $next): Response
    {
        if ($this->echo->active()) {
            $run = Connector::cleanRun($request->headers->get(Wire::RUN_HEADER));
            if ($run === '') {
                $run = Connector::cleanRun($request->cookies->get(Wire::RUN_HEADER));
            }
            if ($run !== '') {
                // REMOTE_ADDR as PHP saw it, not getClientIp(), which trusts
                // whatever proxies the app trusts.
                $forwarded = $request->headers->has('X-Forwarded-For') || $request->headers->has('Forwarded') || $request->headers->has('X-Real-IP');
                $run = $this->echo->acceptRun($run, (string) $request->server('REMOTE_ADDR'), $forwarded);
            }
            if ($run !== '') {
                $this->echo->remember($run);
            }
        }

        return $next($request);
    }
}
