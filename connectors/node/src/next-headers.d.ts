// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// The one Next.js API the connector uses, declared here so Next.js need not
// be installed to build the package.
declare module 'next/headers' {
  export function headers(): Promise<{ get(name: string): string | null }>;
}
