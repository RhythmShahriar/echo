<?php

// The testbed's Laravel app: a subscription box shop. POST /orders with
// email and phone queues the order confirmation and texts the customer
// through Twilio's API, as a checkout would.

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
