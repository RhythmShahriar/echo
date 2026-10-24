<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// The testbed's Laravel app: a subscription box shop. POST /orders with
// email and phone queues the order confirmation and texts the customer
// through Twilio's API, as a checkout would. POST /newsletter with email
// sends the monthly newsletter right away, with its catalogue attached.

use App\Mail\OrderConfirmation;
use Illuminate\Http\Request;
use Illuminate\Support\Facades\Http;
use Illuminate\Support\Facades\Mail;
use Illuminate\Support\Facades\Route;

Route::get('/', fn () => 'Bean & Leaf subscriptions (Laravel). POST /orders with email and phone.');

Route::post('/orders', function (Request $request) {
    $order = random_int(5000, 9999);
    Mail::to($request->string('email')->toString())->queue(new OrderConfirmation($order));

    $account = 'AC'.str_repeat('0', 32);
    $sms = Http::asForm()->withBasicAuth($account, str_repeat('d', 32))
        ->post("https://api.twilio.com/2010-04-01/Accounts/{$account}/Messages.json", [
            'To' => $request->string('phone')->toString(),
            'From' => '+15555550002',
            'Body' => "Bean & Leaf: your subscription box #{$order} is confirmed.",
        ]);

    return ['ok' => true, 'order' => $order, 'sms' => $sms->status()];
})->withoutMiddleware(array_filter([
    // An API-style endpoint: no CSRF token (Laravel 13 renamed the middleware).
    'Illuminate\Foundation\Http\Middleware\PreventRequestForgery',
    'Illuminate\Foundation\Http\Middleware\ValidateCsrfToken',
], 'class_exists'));

// Environment scenario e06 (testbed/scenarios.yaml): while /state/e06
// exists the newsletter is 2 MB of HTML with a 5 MB catalogue, which the
// connector must still hand to Echo within its 2 s timeout. Sent now, not
// queued, so a failed hand-over shows as this request's error.
Route::post('/newsletter', function (Request $request) {
    $large = file_exists('/state/e06');
    $row = '<tr><td style="padding:8px">Single origin of the month: washed Ethiopian, notes of jasmine and peach.</td></tr>';
    $html = '<html><body><table>'.str_repeat($row, $large ? intdiv(2 * 1024 * 1024, strlen($row)) : 20).'</table></body></html>';
    $catalogue = "%PDF-1.4\n".str_repeat("% Bean & Leaf catalogue\n", $large ? intdiv(5 * 1024 * 1024, 24) : 40)."%%EOF\n";
    try {
        Mail::html($html, function ($message) use ($request, $catalogue) {
            $message->to($request->string('email')->toString())
                ->subject('The Bean & Leaf newsletter')
                ->attachData($catalogue, 'catalogue.pdf', ['mime' => 'application/pdf']);
        });
    } catch (Throwable $e) {
        return response(['ok' => false, 'error' => $e->getMessage()], 500);
    }

    return ['ok' => true, 'bytes' => strlen($html) + strlen($catalogue)];
})->withoutMiddleware(array_filter([
    'Illuminate\Foundation\Http\Middleware\PreventRequestForgery',
    'Illuminate\Foundation\Http\Middleware\ValidateCsrfToken',
], 'class_exists'));
