// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

export async function register() {
  const { register } = await import('@rhythmsh/echo/next');
  await register();
}
