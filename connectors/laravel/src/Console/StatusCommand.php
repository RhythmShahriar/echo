<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

namespace Rhythm\Echo\Console;

use Illuminate\Console\Command;
use Rhythm\Echo\Connector;
use Rhythm\Echo\EchoUnreachable;

/**
 * Says whether the connector is on, where Echo is and which mode it runs
 * in. Exits non-zero when it is off or Echo doesn't answer, so
 * `rhythm echo doctor` can rely on the exit code.
 */
class StatusCommand extends Command
{
    protected $signature = 'echo:status {--json : Print JSON for rhythm echo doctor}';

    protected $description = 'Show whether Rhythm Echo captures this site and ping Echo';

    public function handle(Connector $echo): int
    {
        $reason = $echo->inactiveReason();
        $status = [
            'active' => $reason === null,
            'reason' => $reason,
            'url' => $echo->client()->url(),
            'fallback' => $echo->sealed() ? 'seal' : 'observe',
            'run' => $echo->run(),
            'reachable' => false,
            'mode' => null,
            'version' => null,
            'error' => null,
        ];
        try {
            $health = $echo->client()->health();
            $status['reachable'] = true;
            $status['mode'] = $health['mode'] ?? null;
            $status['version'] = $health['version'] ?? null;
        } catch (EchoUnreachable $e) {
            $status['error'] = $e->getMessage();
        }

        if ($this->option('json')) {
            $this->line((string) json_encode($status, JSON_UNESCAPED_SLASHES | JSON_PRETTY_PRINT));
        } else {
            $this->line('Rhythm Echo connector');
            $this->line('  active:    '.($status['active'] ? 'yes' : 'no, '.$reason));
            $this->line('  url:       '.$status['url']);
            $this->line('  echo:      '.($status['reachable'] ? 'reachable, mode '.$status['mode'] : 'unreachable: '.$status['error']));
            $this->line('  fallback:  '.$status['fallback'].' (used only when Echo is unreachable)');
            if ($status['run'] !== '') {
                $this->line('  run:       '.$status['run']);
            }
        }

        return $status['active'] && $status['reachable'] ? self::SUCCESS : self::FAILURE;
    }
}
