<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

namespace Rhythm\Echo\Console;

use Illuminate\Console\Command;
use Illuminate\Http\Client\ConnectionException;
use Illuminate\Mail\Message;
use Illuminate\Support\Facades\Http;
use Illuminate\Support\Facades\Mail;
use Rhythm\Echo\Connector;
use Throwable;

/**
 * Sends one mail and one HTTP call through the app's normal paths (the
 * default mailer, the Http facade), so `rhythm echo doctor` can check both
 * arrive. The targets are under .invalid: even unsealed, nothing can be
 * delivered.
 */
class CanaryCommand extends Command
{
    public const URL = 'https://canary.echo.invalid/';

    public const TO = 'canary@echo.invalid';

    protected $signature = 'echo:canary {--json : Print JSON for rhythm echo doctor}';

    protected $description = 'Send one test mail and one HTTP call through Rhythm Echo';

    public function handle(Connector $echo): int
    {
        $result = ['active' => $echo->active(), 'run' => $echo->run(), 'mail' => null, 'http' => null];
        if (! $result['active']) {
            $result['mail'] = $result['http'] = 'skipped: '.$echo->inactiveReason();

            return $this->finish($result, false);
        }

        try {
            Mail::raw("Rhythm Echo canary.\n\nIf you can read this outside Echo, the seal leaks.", function (Message $m) {
                $m->to(self::TO)->subject('Rhythm Echo canary');
            });
            $result['mail'] = 'ok';
        } catch (Throwable $e) {
            $result['mail'] = 'failed: '.$e->getMessage();
        }

        try {
            $r = Http::timeout(5)->post(self::URL, ['canary' => true, 'tool' => 'rhythm-echo']);
            $result['http'] = 'ok, '.$r->status();
        } catch (ConnectionException $e) {
            // Unsealed, .invalid never resolves: the capture still arrived.
            $result['http'] = 'not answered: '.$e->getMessage();
        } catch (Throwable $e) {
            $result['http'] = 'failed: '.$e->getMessage();
        }

        return $this->finish($result, $result['mail'] === 'ok' && str_starts_with((string) $result['http'], 'ok'));
    }

    /**
     * @param  array<string, mixed>  $result
     */
    private function finish(array $result, bool $ok): int
    {
        if ($this->option('json')) {
            $this->line((string) json_encode($result + ['ok' => $ok], JSON_UNESCAPED_SLASHES | JSON_PRETTY_PRINT));
        } else {
            $this->line('mail: '.$result['mail']);
            $this->line('http: '.$result['http']);
        }

        return $ok ? self::SUCCESS : self::FAILURE;
    }
}
