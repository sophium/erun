#!/usr/bin/env node
// Authenticated REST via a saved Playwright storageState session; see SKILL.md.
import { request } from 'playwright';
import { argv, env, exit, stdout } from 'node:process';
import { chmodSync, existsSync, writeFileSync } from 'node:fs';

// storageState({ path }) creates the file under the process umask, which leaves
// live session cookies readable by other local users.
function writeSession(path, state) {
  if (existsSync(path)) {
    chmodSync(path, 0o600);
  }
  writeFileSync(path, `${JSON.stringify(state, null, 2)}\n`, { mode: 0o600 });
}

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
  writeSession(sessionPath, await context.storageState());
  const text = await response.text();
  stdout.write(text.endsWith('\n') ? text : `${text}\n`);
  if (!response.ok()) {
    console.error(`HTTP ${response.status()} ${response.statusText()}`);
    exit(1);
  }
} finally {
  await context.dispose();
}
