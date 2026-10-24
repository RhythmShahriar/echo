<?php

namespace Rhythm\Echo\Tests\Fixtures;

use Illuminate\Bus\Queueable;
use Illuminate\Contracts\Queue\ShouldQueue;
use Illuminate\Foundation\Bus\Dispatchable;
use Illuminate\Queue\InteractsWithQueue;
use Illuminate\Support\Facades\Http;

class PingTwilio implements ShouldQueue
{
    use Dispatchable, InteractsWithQueue, Queueable;

    public function handle(): void
    {
        Http::asForm()->withBasicAuth('AC'.str_repeat('0', 32), 'fake-auth-token')
            ->post('https://api.twilio.com/2010-04-01/Accounts/AC'.str_repeat('0', 32).'/Messages.json', [
                'To' => '+15555550100', 'From' => '+15555550000', 'Body' => 'Your code is 123456',
            ]);
    }
}
