<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

namespace Rhythm\Echo\Tests;

use Illuminate\Support\Facades\Mail;

/**
 * email.attachments: filled only when the capture carries no raw MIME, as
 * for a mail over the 2 MiB raw cap. Echo reads the files from the MIME
 * otherwise.
 */
class AttachmentTest extends TestCase
{
    /** A PDF big enough to push the mail past the raw cap. */
    private static function pdf(): string
    {
        return "%PDF-1.7\n".str_repeat("\x07", 2_300_000)."\n%%EOF\n";
    }

    public function test_a_mail_over_the_cap_lists_its_pdf(): void
    {
        $pdf = self::pdf();
        Mail::raw('Your invoice', fn ($m) => $m->to('a@example.test')->subject('Invoice')
            ->attachData($pdf, 'invoice-42.pdf', ['mime' => 'application/pdf']));

        $email = $this->captures('email')[0]['email'];
        $this->assertArrayNotHasKey('raw', $email);
        $this->assertSame([[
            'name' => 'invoice-42.pdf',
            'type' => 'application/pdf',
            'size' => strlen($pdf),
            'sha256' => hash('sha256', $pdf),
        ]], $email['attachments']);
    }

    public function test_inline_images_are_not_attachments(): void
    {
        $pdf = self::pdf();
        Mail::html('<img src="cid:logo">', fn ($m) => $m->to('a@example.test')->subject('Inline')
            ->attachData($pdf, 'invoice-42.pdf', ['mime' => 'application/pdf'])
            ->embedData('png', 'logo.png', 'image/png'));

        $this->assertSame(['invoice-42.pdf'], array_column($this->captures('email')[0]['email']['attachments'], 'name'));
    }

    public function test_a_mail_over_the_cap_without_files_has_no_field(): void
    {
        Mail::raw(str_repeat('a', 2_300_000), fn ($m) => $m->to('a@example.test')->subject('Long'));

        $email = $this->captures('email')[0]['email'];
        $this->assertArrayNotHasKey('raw', $email);
        $this->assertArrayNotHasKey('attachments', $email);
    }

    public function test_a_mail_with_its_mime_has_no_field(): void
    {
        Mail::raw('Your invoice', fn ($m) => $m->to('a@example.test')->subject('Invoice')
            ->attachData('%PDF-1.7 small', 'invoice-1.pdf', ['mime' => 'application/pdf']));

        $email = $this->captures('email')[0]['email'];
        $this->assertStringContainsString('invoice-1.pdf', $email['raw']);
        $this->assertArrayNotHasKey('attachments', $email);
    }
}
