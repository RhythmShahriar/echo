// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package runner

import (
	"context"
	"os"
	"os/exec"
	"time"
)

// waitDelay is how long Wait waits for a canceled command's output pipes
// to close once its process is gone. A grandchild that left the process
// group can hold stderr open; without a delay Wait would block until it
// exits.
const waitDelay = 2 * time.Second

// Shell returns a command that runs cmd in the platform's shell (sh -c,
// or cmd /C on Windows) with env added to Echo's own environment. When
// ctx is canceled the whole tree the shell started is stopped, not only
// the shell: killing sh alone leaves "sleep 25" running and Wait blocked
// on its stderr until it exits.
func Shell(ctx context.Context, cmd string, env []string) *exec.Cmd {
	c := shell(ctx, cmd)
	c.Env = append(os.Environ(), env...)
	c.WaitDelay = waitDelay
	return c
}
