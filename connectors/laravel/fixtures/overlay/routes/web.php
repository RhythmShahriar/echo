<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// The end-to-end fixture's one action: an order is placed. It sends a
// Mailable, texts the customer through Twilio's API and queues a
// notification, as a real checkout would.

use App\Mail\OrderShipped;
use App\Notifications\OrderReceived;
use Illuminate\Support\Facades\Http;
use Illuminate\Support\Facades\Mail;
use Illuminate\Support\Facades\Notification;
use Illuminate\Support\Facades\Route;

Route::get('/', fn () => 'echo e2e fixture');

Route::get('/echo-e2e', function () {
    Mail::to('ada@example.test')->send(new OrderShipped);

    $account = 'AC'.str_repeat('0', 32);
    $sms = Http::asForm()->withBasicAuth($account, 'fake-auth-token')
        ->post("https://api.twilio.com/2010-04-01/Accounts/{$account}/Messages.json", [
            'To' => '+15555550100',
            'From' => '+15555550000',
            'Body' => 'Order #1001 is confirmed',
        ]);

    Notification::route('mail', 'ada@example.test')->notify(new OrderReceived);

    return ['twilio' => $sms->status(), 'twilio_body' => $sms->json()];
});
