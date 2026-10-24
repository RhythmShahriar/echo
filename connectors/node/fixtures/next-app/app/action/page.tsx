// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

import { notifyTeam } from './actions';

export default function ActionPage() {
  return (
    <form
      action={async () => {
        'use server';
        await notifyTeam();
      }}
    >
      <button type="submit">Notify</button>
    </form>
  );
}
