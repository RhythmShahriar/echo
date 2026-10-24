<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Rhythm Echo connector. Every key can be set from the environment, so a
// staging box needs no published config file.
return [

    // On or off. Left unset (null), the connector runs in every environment
    // except "production" and "testing": the app's own test suite keeps its
    // Mail::fake and Http::fake untouched. Set true to run it in "testing".
    'enabled' => env('RHYTHM_ECHO_ENABLED'),

    // Where `rhythm-echo up` listens.
    'url' => env('RHYTHM_ECHO_URL', 'http://127.0.0.1:8025'),

    // Sent as X-Echo-Token when Echo listens beyond loopback, unless a
    // write token is set. X-Echo-Run signatures are checked with it.
    'token' => env('RHYTHM_ECHO_TOKEN'),

    // A token that may only send captures (a hub's per-site token). Sent
    // as X-Echo-Token instead of the token; with no token it also checks
    // X-Echo-Run signatures.
    'write_token' => env('RHYTHM_ECHO_WRITE_TOKEN'),

    // This site's ID when one Echo serves many sites, sent as source.site.
    'site_id' => env('RHYTHM_ECHO_SITE_ID'),

    // Run even when APP_ENV is production. Only for a production-like
    // staging box that you are sure is not production.
    'force' => (bool) env('RHYTHM_ECHO_FORCE', false),

    // What to assume when Echo can't be reached. "seal" fails closed:
    // mail and HTTP calls fail instead of leaving. "observe" lets them
    // through. When Echo answers, its own mode decides.
    'mode' => env('RHYTHM_ECHO_MODE', 'seal'),

    // Seconds to wait for Echo. Echo runs next to the site, so keep it short.
    'timeout' => (float) env('RHYTHM_ECHO_TIMEOUT', 2.0),

    // Also capture Guzzle clients the container builds (type-hinted
    // GuzzleHttp\Client, such as the Slack webhook channel) and the Vonage
    // SMS client. Clients made with `new Client()` are not covered: route
    // them through Echo's proxy with HTTPS_PROXY.
    'guzzle' => (bool) env('RHYTHM_ECHO_GUZZLE', true),

];
