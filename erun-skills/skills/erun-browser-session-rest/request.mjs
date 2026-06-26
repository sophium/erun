#!/usr/bin/env node
// Authenticated REST via a saved browser session (Playwright storageState).
//
// Host-parameterized: no baked-in host, credentials, or IdP. The base URL comes
// from ERUN_REST_BASE_URL or --base; the saved session from ERUN_REST_SESSION
// or --session (default ./session.json), produced by save-session.mjs. The
// session is rolled forward (re-saved) after each call so refreshed auth
// cookies persist for the next one. See SKILL.md.
//
// Usage:
//   ERUN_REST_BASE_URL=https://host node request.mjs GET /rest/api/2/myself
//   node request.mjs --base https://host POST /path '{"json":"body"}'
import { request } from 'playwright';
import { argv, env, exit, stdout } from 'node:process';

function usage(message) {
  if (message) {
    console.error(`error: ${message}`);
  }
  console.error(
    'usage: [ERUN_REST_BASE_URL=https://host] node request.mjs ' +
      '[--base URL] [--session FILE] METHOD PATH [JSON_BODY]',
  );
  exit(2);
}

const args = argv.slice(2);
let base = env.ERUN_REST_BASE_URL ?? '';
let sessionPath = env.ERUN_REST_SESSION ?? 'session.json';
const positional = [];
for (let i = 0; i < args.length; i += 1) {
  if (args[i] === '--base') {
    base = args[(i += 1)] ?? '';
  } else if (args[i] === '--session') {
    sessionPath = args[(i += 1)] ?? sessionPath;
  } else {
    positional.push(args[i]);
  }
}

const [method, path, body] = positional;
if (!base) {
  usage('a base URL is required (ERUN_REST_BASE_URL or --base)');
}
if (!method || !path) {
  usage('METHOD and PATH are required');
}

const url = base.replace(/\/+$/, '') + (path.startsWith('/') ? path : `/${path}`);

let context;
try {
  context = await request.newContext({ storageState: sessionPath });
} catch (error) {
  console.error(
    `error: could not load session ${sessionPath} — run save-session.mjs first ` +
      `(${error instanceof Error ? error.message : String(error)})`,
  );
  exit(2);
}

try {
  const response = await context.fetch(url, {
    method: method.toUpperCase(),
    headers: {
      Accept: 'application/json',
      ...(body ? { 'Content-Type': 'application/json' } : {}),
    },
    ...(body ? { data: body } : {}),
  });
  // Roll the session forward so any refreshed auth cookies are kept.
  await context.storageState({ path: sessionPath });
  const text = await response.text();
  stdout.write(text.endsWith('\n') ? text : `${text}\n`);
  if (!response.ok()) {
    console.error(`HTTP ${response.status()} ${response.statusText()}`);
    exit(1);
  }
} finally {
  await context.dispose();
}
