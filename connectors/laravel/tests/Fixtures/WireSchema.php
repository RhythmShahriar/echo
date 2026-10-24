<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

namespace Rhythm\Echo\Tests\Fixtures;

/**
 * Echo's wire schema (wire/schema.json) and a validator for the subset of
 * JSON Schema it uses, a port of conformance/schema.go. The fake Echo
 * checks every capture with it, so the suite fails on a capture Echo's own
 * schema would refuse.
 */
final class WireSchema
{
    /** @var array<string, array<string, mixed>>|null */
    private static ?array $defs = null;

    /** The schema file: ECHO_WIRE_SCHEMA, else wire/schema.json in Echo's repository. */
    public static function path(): string
    {
        $env = getenv('ECHO_WIRE_SCHEMA');

        return is_string($env) && $env !== '' ? $env : dirname(__DIR__, 4).'/wire/schema.json';
    }

    public static function available(): bool
    {
        return is_readable(self::path());
    }

    /**
     * What is wrong with a capture, as "path: problem" lines.
     *
     * @return list<string>
     */
    public static function capture(mixed $doc): array
    {
        return self::problems('Capture', $doc);
    }

    /**
     * What is wrong with Echo's answer.
     *
     * @return list<string>
     */
    public static function decision(mixed $doc): array
    {
        return self::problems('Decision', $doc);
    }

    /**
     * @return list<string>
     */
    private static function problems(string $def, mixed $doc): array
    {
        if (self::$defs === null) {
            if (! self::available()) {
                return [];
            }
            $schema = json_decode((string) file_get_contents(self::path()), true);
            self::$defs = is_array($schema) && is_array($schema['$defs'] ?? null) ? $schema['$defs'] : [];
        }
        $version = is_array($doc) && is_int($doc['v'] ?? null) ? $doc['v'] : 2;
        $out = [];
        self::value(strtolower($def), $doc, ['$ref' => '#/$defs/'.$def], $version, $out);

        return $out;
    }

    /**
     * @param  array<string, mixed>  $n
     * @param  list<string>  $out
     */
    private static function value(string $path, mixed $v, array $n, int $version, array &$out): void
    {
        if (is_string($n['$ref'] ?? null)) {
            $n = self::$defs[substr($n['$ref'], strlen('#/$defs/'))] ?? [];
        }
        switch ($n['type'] ?? '') {
            case 'object':
                // PHP decodes {} and [] alike: an empty array passes as an empty object.
                if (! is_array($v) || ($v !== [] && array_is_list($v))) {
                    $out[] = "$path: want an object, got ".self::kind($v);

                    return;
                }
                $props = is_array($n['properties'] ?? null) ? $n['properties'] : [];
                foreach ((array) ($n['required'] ?? []) as $r) {
                    if (! array_key_exists($r, $v)) {
                        $out[] = "$path.$r: missing (required)";
                    }
                }
                $keys = array_map('strval', array_keys($v));
                sort($keys);
                foreach ($keys as $k) {
                    $p = $props[$k] ?? null;
                    if (! is_array($p)) {
                        $extra = $n['additionalProperties'] ?? true;
                        if (is_array($extra)) {
                            self::value("$path.$k", $v[$k], $extra, $version, $out);
                        } elseif ($extra === false) {
                            $out[] = "$path.$k: not part of the wire protocol";
                        }

                        continue;
                    }
                    if (is_int($p['x-since'] ?? null) && $p['x-since'] > $version) {
                        $out[] = "$path.$k: new in wire {$p['x-since']} while this capture says v $version: leave it out for an older Echo";

                        continue;
                    }
                    self::value("$path.$k", $v[$k], $p, $version, $out);
                }

                return;
            case 'array':
                if (! is_array($v) || ! array_is_list($v)) {
                    $out[] = "$path: want an array, got ".self::kind($v);

                    return;
                }
                foreach ($v as $i => $e) {
                    self::value("{$path}[$i]", $e, is_array($n['items'] ?? null) ? $n['items'] : [], $version, $out);
                }

                return;
            case 'string':
                if (! is_string($v)) {
                    $out[] = "$path: want a string, got ".self::kind($v);

                    return;
                }
                if (is_array($n['enum'] ?? null) && ! in_array($v, $n['enum'], true)) {
                    $out[] = "$path: \"$v\" is not one of ".implode(', ', $n['enum']);
                }

                return;
            case 'integer':
                if (! is_int($v)) {
                    $out[] = "$path: want an integer, got ".self::kind($v);

                    return;
                }
                if (is_int($n['minimum'] ?? null) && $v < $n['minimum']) {
                    $out[] = "$path: $v is below {$n['minimum']}";
                }
                if (is_int($n['maximum'] ?? null) && $v > $n['maximum']) {
                    $out[] = "$path: $v is above {$n['maximum']}, the newest this Echo speaks";
                }

                return;
            case 'boolean':
                if (! is_bool($v)) {
                    $out[] = "$path: want true or false, got ".self::kind($v);
                }
        }
    }

    private static function kind(mixed $v): string
    {
        return match (true) {
            $v === null => 'null',
            is_array($v) => array_is_list($v) && $v !== [] ? 'an array' : 'an object',
            is_string($v) => 'a string',
            is_int($v), is_float($v) => 'a number',
            is_bool($v) => 'a boolean',
            default => get_debug_type($v),
        };
    }
}
