<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// A fake Echo for the tests, run with `php -S 127.0.0.1:PORT echo-server.php`.
// It speaks the capture protocol: captures are appended to
// $ECHO_STATE/captures.jsonl, the mode is read from $ECHO_STATE/mode and
// the wire version it speaks from $ECHO_STATE/wire (default 2). Like Echo's
// conformance stand-in it is strict: a capture that breaks wire/schema.json
// is answered 400 and appended to $ECHO_STATE/invalid.jsonl, which
// TestCase fails on.

require __DIR__.'/WireSchema.php';

$state = getenv('ECHO_STATE') ?: sys_get_temp_dir().'/rhythm-echo-test';
$path = parse_url($_SERVER['REQUEST_URI'], PHP_URL_PATH);
$mode = is_file("$state/mode") ? trim((string) file_get_contents("$state/mode")) : 'seal';
$wire = is_file("$state/wire") ? (int) file_get_contents("$state/wire") : 2;

function answer(int $status, mixed $body, string $type = 'application/json'): void
{
    http_response_code($status);
    header("Content-Type: $type");
    echo is_string($body) ? $body : json_encode($body);
}

if (is_file("$state/token") && ($_SERVER['HTTP_X_ECHO_TOKEN'] ?? '') !== trim((string) file_get_contents("$state/token"))) {
    answer(401, 'missing or wrong X-Echo-Token', 'text/plain');

    return;
}

// $ECHO_STATE/fail makes Echo fail the way a conformance vector says:
// 5xx answers 503, timeout answers too late for the connector.
$fail = is_file("$state/fail") ? trim((string) file_get_contents("$state/fail")) : '';
if ($fail !== '' && str_starts_with((string) $path, '/v1/')) {
    if ($fail === 'timeout') {
        usleep(1_500_000);
    }
    answer(503, 'echo is broken', 'text/plain');

    return;
}

if ($path === '/target') { // a real endpoint for allowed requests
    answer(200, ['pong' => true, 'got' => file_get_contents('php://input')]);

    return;
}

if ($path === '/v1/health') {
    file_put_contents("$state/health", '1', FILE_APPEND | LOCK_EX);
    answer(200, ['tool' => 'rhythm-echo', 'version' => 'test', 'mode' => $mode, 'wire' => $wire]);

    return;
}

if ($path !== '/v1/capture' || $_SERVER['REQUEST_METHOD'] !== 'POST') {
    answer(404, 'not found', 'text/plain');

    return;
}

$c = json_decode((string) file_get_contents('php://input'), true);
if (is_array($c) && is_int($c['v'] ?? null) && $c['v'] > $wire && $c['v'] <= 2) {
    answer(400, "this connector is newer than Echo; run: rhythm update\n", 'text/plain');

    return;
}
$problems = Rhythm\Echo\Tests\Fixtures\WireSchema::capture($c);
if ($problems !== []) {
    file_put_contents("$state/invalid.jsonl", json_encode(['problems' => $problems, 'capture' => $c])."\n", FILE_APPEND | LOCK_EX);
    answer(400, 'conformance: '.implode('; ', $problems), 'text/plain');

    return;
}
$id = $c['id'] ?? bin2hex(random_bytes(6));
$c['_id'] = $id;
$c['_headers'] = ['token' => $_SERVER['HTTP_X_ECHO_TOKEN'] ?? ''];
file_put_contents("$state/captures.jsonl", json_encode($c)."\n", FILE_APPEND | LOCK_EX);

if ($c['kind'] === 'email') {
    answer(200, ['id' => $id, 'mode' => $mode, 'action' => $mode === 'observe' ? 'allow' : 'fake']);

    return;
}

$host = strtolower((string) parse_url($c['http']['url'] ?? '', PHP_URL_HOST));
$site = strtolower((string) ($c['source']['host'] ?? ''));
$d = ['id' => $id, 'mode' => $mode];
if (($c['phase'] ?? '') === 'after') {
    $d['action'] = 'allow';
} elseif ($host === 'blocked.test') {
    $d['action'] = 'block';
} elseif ($mode === 'observe' || $host === '127.0.0.1' || $host === 'localhost' || $host === $site) {
    $d['action'] = 'allow';
} else {
    $d['action'] = 'fake';
    $d['response'] = match (true) {
        str_contains($host, 'twilio') => ['status' => 201, 'headers' => ['Content-Type' => 'application/json'],
            'body' => json_encode(['sid' => 'SM'.str_repeat('0', 32), 'status' => 'queued'])],
        str_contains($host, 'nexmo') || str_contains($host, 'vonage') => ['status' => 200, 'headers' => ['Content-Type' => 'application/json'],
            'body' => json_encode(['message-count' => '1', 'messages' => [['to' => '15555550100', 'message-id' => '0A0000000000000A',
                'status' => '0', 'remaining-balance' => '1.00', 'message-price' => '0.01', 'network' => '12345']]])],
        default => ['status' => 200, 'headers' => ['Content-Type' => 'application/json'], 'body' => '{"ok":true}'],
    };
}
answer(200, $d);
