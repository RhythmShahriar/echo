<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

namespace Rhythm\Echo;

/**
 * Constants of Echo's capture protocol (wire/wire.go in the Echo repo and
 * its JSON Schema wire/schema.json). Keep them in step with it: the test
 * suite checks every capture against the schema.
 */
final class Wire
{
    /** The wire version captures are sent in. */
    public const VERSION = 2;

    /** The oldest wire version, spoken to an Echo that says so in /v1/health. */
    public const MIN_VERSION = 1;

    /** Carries "<run>/<action>" from whatever triggered the request (header or cookie). */
    public const RUN_HEADER = 'X-Echo-Run';

    /** Authenticates the connector when Echo listens beyond loopback. */
    public const TOKEN_HEADER = 'X-Echo-Token';

    /** Set by Echo's action runner on commands it runs: no request to read. */
    public const RUN_ENV = 'RHYTHM_ECHO_RUN';

    /**
     * The longest run value taken, in bytes, signature included; the same
     * in every connector. A longer one is refused whole: cutting it would
     * cut the signature too.
     */
    public const MAX_RUN = 512;

    /** An HTTP capture before sending: Echo decides. */
    public const BEFORE = 'before';

    /** A capture after a real send: its response or error. */
    public const AFTER = 'after';

    /** Decision: send it for real. */
    public const ALLOW = 'allow';

    /** Decision: don't send; answer with Echo's response. */
    public const FAKE = 'fake';

    /** Decision: don't send; fail it. */
    public const BLOCK = 'block';

    /** Mode: nothing leaves except allowed hosts. */
    public const SEAL = 'seal';

    /** Mode: everything is recorded and sent for real. */
    public const OBSERVE = 'observe';

    /** A run signature: 16 bytes in unpadded base64url, 22 characters. */
    private const SIGNATURE = '/^[A-Za-z0-9_-]{22}$/';

    /**
     * The capture in wire version $v: wire 1 has no source.site and no
     * email.attachments, so an Echo that speaks only wire 1 gets neither.
     *
     * @param  array<string, mixed>  $capture
     * @return array<string, mixed>
     */
    public static function forVersion(array $capture, int $v): array
    {
        $capture['v'] = $v;
        if ($v < 2) {
            if (is_array($capture['source'] ?? null)) {
                unset($capture['source']['site']);
            }
            if (is_array($capture['email'] ?? null)) {
                unset($capture['email']['attachments']);
            }
        }

        return $capture;
    }

    /**
     * Port of wire.SignRun for "<run>/<action>": the value with its
     * signature appended, or unsigned without a token.
     */
    public static function signRun(string $token, string $run): string
    {
        return $token === '' ? $run : $run.'.'.self::runSignature($token, $run);
    }

    /**
     * Port of wire.VerifyRun: the run and action of a value and whether it
     * carries a valid signature for the token. An unsigned value keeps its
     * run and action with ok false; a signature that does not verify gives
     * nothing.
     *
     * @return array{string, string, bool}
     */
    public static function verifyRun(string $token, string $value): array
    {
        [$v, $sig] = self::splitSignature($value);
        $slash = strpos($v, '/');
        if ($slash === false || $slash === 0) {
            return ['', '', false];
        }
        $run = substr($v, 0, $slash);
        $action = substr($v, $slash + 1);
        if ($sig === '') {
            return [$run, $action, false];
        }
        if ($token === '' || ! hash_equals(self::runSignature($token, $v), $sig)) {
            return ['', '', false];
        }

        return [$run, $action, true];
    }

    /**
     * Splits "<run>/<action>.<sig>" into the value and the signature ("" for
     * none). The signature's shape tells it from an action with a dot in it
     * (order.paid).
     *
     * @return array{string, string}
     */
    public static function splitSignature(string $value): array
    {
        $dot = strrpos($value, '.');
        if ($dot !== false && preg_match(self::SIGNATURE, substr($value, $dot + 1))) {
            return [substr($value, 0, $dot), substr($value, $dot + 1)];
        }

        return [$value, ''];
    }

    private static function runSignature(string $token, string $v): string
    {
        return rtrim(strtr(base64_encode(substr(hash_hmac('sha256', $v, $token, true), 0, 16)), '+/', '-_'), '=');
    }
}
