<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

namespace Rhythm\Echo\Tests\Fixtures;

use Illuminate\Contracts\Queue\ShouldQueue;

class QueuedOrderNote extends OrderNote implements ShouldQueue
{
}
