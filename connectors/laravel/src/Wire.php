<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

namespace Rhythm\Echo;

/**
 * Constants of Echo's capture protocol (wire/wire.go in the Echo repo).
 * Keep them in step with it: Echo checks the version.
 */
final class Wire
{
    public const VERSION = 1;

    public const RUN_HEADER = 'X-Echo-Run';

    public const TOKEN_HEADER = 'X-Echo-Token';

    /** Set by Echo's action runner on commands it runs: no request to read. */
    public const RUN_ENV = 'RHYTHM_ECHO_RUN';

    public const BEFORE = 'before';

    public const AFTER = 'after';

    public const ALLOW = 'allow';

    public const FAKE = 'fake';

    public const BLOCK = 'block';

    public const SEAL = 'seal';

    public const OBSERVE = 'observe';

    /** A run signature: 16 bytes in unpadded base64url, 22 characters. */
    private const SIGNATURE = '/^[A-Za-z0-9_-]{22}$/';

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
