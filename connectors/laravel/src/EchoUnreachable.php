<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

namespace Rhythm\Echo;

use RuntimeException;

/** Echo didn't answer, or answered with something that isn't a decision. */
final class EchoUnreachable extends RuntimeException
{
}
