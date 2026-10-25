// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// The JSON protocol between Echo and connectors, mirrored from Echo's
// wire/wire.go (and its JSON Schema, wire/schema.json). Keep them in step:
// the test suite checks every capture against the schema.

/** The wire version captures are sent in. */
export const WIRE_VERSION = 2;
/** The oldest wire version, spoken to an Echo that says so in /v1/health. */
export const WIRE_MIN_VERSION = 1;

/** Carries "<run>/<action>" from whatever triggered the work. */
export const RUN_HEADER = 'X-Echo-Run';
/** Authenticates connectors when Echo listens beyond loopback. */
export const TOKEN_HEADER = 'X-Echo-Token';

/** Phase of an HTTP capture: before it is sent (Echo decides) or after (its response). */
export type Phase = 'before' | 'after';
/** What Echo decided: send it for real, answer with Echo's response or fail it. */
export type Action = 'allow' | 'fake' | 'block';

/** Source describes the connector and what it hooked. */
export interface Source {
  /** node for this connector. */
  via: string;
  stack?: string;
  plugin?: string;
  hook?: string;
  host?: string;
  /** The site's ID when one Echo serves many sites. New in wire 2. */
  site?: string;
}

/** Email is a message handed to the app's mailer. */
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
  /** The files the mail carries, for a capture without raw MIME. Never their content. New in wire 2. */
  attachments?: Attachment[];
}

/** Attachment describes one attached file: what is known about it, never its content. */
export interface Attachment {
  name?: string;
  /** MIME type, application/pdf. */
  type?: string;
  /** Decoded size in bytes. */
  size?: number;
  /** Hex SHA-256 of the decoded content. */
  sha256?: string;
}

/** HTTP is an outbound request and, after sending, its response. */
export interface HTTP {
  method: string;
  url: string;
  /** Lowercased names, credentials masked (see maskHeaders). */
  headers?: Record<string, string>;
  body?: string;
  /** After: the response status. */
  status?: number;
  /** After: the response body. */
  response?: string;
}

/** Capture is one email or HTTP request reported to Echo. */
export interface Capture {
  /** The wire version this capture follows. */
  v: number;
  kind: 'email' | 'http';
  phase?: Phase;
  /** After: the ID Echo returned for the before capture. */
  id?: string;
  /** "<run>/<action>", from X-Echo-Run. */
  run?: string;
  source: Source;
  email?: Email;
  http?: HTTP;
  /** The send failed: the error, as text. */
  error?: string;
}

/** Response is what the app gets instead of the real service's answer. */
export interface Response {
  status: number;
  headers?: Record<string, string>;
  body?: string;
}

/** Decision is Echo's answer to a capture. */
export interface Decision {
  id: string;
  /** seal or observe: remembered, so a later missing decision fails the right way. */
  mode: string;
  action?: Action;
  response?: Response;
}

/**
 * downgrade returns c as wire 1 has it, without the fields wire 2 added,
 * for an Echo that speaks only wire 1.
 */
export function downgrade<T extends Omit<Capture, 'v'>>(c: T): T {
  const out = { ...c, source: { ...c.source } };
  delete out.source.site;
  if (out.email?.attachments) {
    out.email = { ...out.email };
    delete out.email.attachments;
  }
  return out;
}
