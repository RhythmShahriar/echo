<?php

namespace Rhythm\Echo\Tests\Fixtures;

use Illuminate\Contracts\Queue\ShouldQueue;

class QueuedOrderNote extends OrderNote implements ShouldQueue
{
}
