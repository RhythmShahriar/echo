// The JSON protocol between Echo and connectors, mirrored from Echo's
// wire/wire.go. Keep the two in step: Echo rejects captures from a newer
// wire version.

export const WIRE_VERSION = 1;

/** Carries "<run>/<action>" from whatever triggered the work. */
export const RUN_HEADER = 'X-Echo-Run';
/** Authenticates connectors when Echo listens beyond loopback. */
export const TOKEN_HEADER = 'X-Echo-Token';

export type Phase = 'before' | 'after';
export type Action = 'allow' | 'fake' | 'block';

export interface Source {
  via: string;
  stack?: string;
  plugin?: string;
  hook?: string;
  host?: string;
}

export interface Email {
  from?: string;
  to?: string[];
  cc?: string[];
  bcc?: string[];
  reply_to?: string;
  subject?: string;
  text?: string;
  html?: string;
  headers?: Record<string, string>;
  raw?: string;
}

export interface HTTP {
  method: string;
  url: string;
  headers?: Record<string, string>;
  body?: string;
  status?: number;
  response?: string;
}

export interface Capture {
  v: number;
  kind: 'email' | 'http';
  phase?: Phase;
  id?: string;
  run?: string;
  source: Source;
  email?: Email;
  http?: HTTP;
  error?: string;
}

export interface Response {
  status: number;
  headers?: Record<string, string>;
  body?: string;
}

export interface Decision {
  id: string;
  mode: string;
  action?: Action;
  response?: Response;
}
