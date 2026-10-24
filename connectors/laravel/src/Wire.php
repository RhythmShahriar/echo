<?php

namespace Rhythm\Echo;

/**
 * Constants of Echo's capture protocol (wire/wire.go in the Echo repo).
 * Keep them in step with it: Echo checks the version.
 */
final class Wire
{
    public const VERSION = 1;

    public const RUN_HEADER = 'X-Echo-Run';

    public const TOKEN_HEADER = 'X-Echo-Token';

    /** Set by Echo's action runner on commands it runs: no request to read. */
    public const RUN_ENV = 'RHYTHM_ECHO_RUN';

    public const BEFORE = 'before';

    public const AFTER = 'after';

    public const ALLOW = 'allow';

    public const FAKE = 'fake';

    public const BLOCK = 'block';

    public const SEAL = 'seal';

    public const OBSERVE = 'observe';
}
