<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

namespace Rhythm\Echo\Tests;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase as Unit;
use Rhythm\Echo\Connector;

class MaskTest extends Unit
{
    /** @return array<string, array{string}> */
    public static function secretNames(): array
    {
        return array_combine($n = [
            'X-Auth-Token', 'x-amz-security-token', 'Private-Token', 'X-Goog-Api-Key', 'X-Shopify-Access-Token',
            'X-WC-Webhook-Signature', 'X-Session-Id', 'X-Db-Password', 'Set-Cookie', 'X-Client-Credential',
        ], array_map(fn ($v) => [$v], $n));
    }

    #[DataProvider('secretNames')]
    public function test_a_credential_header_is_hidden_whole(string $name): void
    {
        $this->assertSame('•••••', Connector::maskHeader($name, 'Zq9TOPSECRETvalue77'));
    }

    /** @return array<string, array{string, string}> */
    public static function plainHeaders(): array
    {
        return [
            'x-request-id' => ['X-Request-Id', 'req-1234567890abcdef'],
            'content-type' => ['Content-Type', 'application/json'],
            'x-echo-run' => ['X-Echo-Run', 'r1/checkout.NhOp3HY0JQ76wSoPnn3sZg'],
        ];
    }

    #[DataProvider('plainHeaders')]
    public function test_other_headers_are_kept(string $name, string $value): void
    {
        $this->assertSame($value, Connector::maskHeader($name, $value));
    }

    public function test_at_most_four_characters_show(): void
    {
        $got = Connector::maskHeader('X-Api-Key', 'abcdefghijklmnopqrstuvwxyz0123456789');
        $this->assertLessThanOrEqual(4, mb_strlen(str_replace('•', '', $got)));
    }

    public function test_authorization_keeps_its_scheme_and_a_stripe_mode(): void
    {
        $this->assertSame('Bearer •••••', Connector::maskHeader('Authorization', 'Bearer xoxb-123456789012'));
        $this->assertSame('Bearer sk_test_•••••', Connector::maskHeader('Authorization', 'Bearer sk_test_abcdefghijkl'));
        $this->assertSame('Basic sk_live_•••••', Connector::maskHeader('Authorization', 'Basic '.base64_encode('sk_live_abcdefghijkl:')));
        $this->assertSame('Basic •••••', Connector::maskHeader('Authorization', 'Basic '.base64_encode('AC0:fake-auth-token')));
        $this->assertSame('•••••', Connector::maskHeader('Proxy-Authorization', 'opaque'));
    }

    public function test_an_empty_value_stays_empty(): void
    {
        $this->assertSame('', Connector::maskHeader('X-Auth-Token', ''));
    }

    public function test_headers_flattens_and_masks(): void
    {
        $this->assertSame(
            ['X-Auth-Token' => '•••••', 'Accept' => 'a, b'],
            Connector::headers(['X-Auth-Token' => ['Zq9TOPSECRETvalue77'], 'Accept' => ['a', 'b']]),
        );
    }
}
