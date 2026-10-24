<?php

namespace Rhythm\Echo;

use RuntimeException;

/** Echo didn't answer, or answered with something that isn't a decision. */
final class EchoUnreachable extends RuntimeException
{
}
