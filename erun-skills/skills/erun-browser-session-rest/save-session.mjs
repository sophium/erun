#!/usr/bin/env node
// Establish and save a browser login session (Playwright storageState) for a
// host whose org blocks API tokens and admin-gates OAuth.
//
// Opens a REAL browser window; YOU log in interactively (SSO + MFA and all),
// then press Enter to save the session cookies. No password is ever read or
// stored — only the resulting session. Host-parameterized: the login URL comes
// from ERUN_REST_LOGIN_URL or --login; the session file from ERUN_REST_SESSION
// or --session (default ./session.json). See SKILL.md, including why the login
// is intentionally manual (SSO-DOM automation is brittle and secret-leaky).
//
// Usage:
//   ERUN_REST_LOGIN_URL=https://host/login node save-session.mjs
//   node save-session.mjs --login https://host/login --session ./session.json
import { chromium } from 'playwright';
import { argv, env, exit, stdin, stdout } from 'node:process';
import { createInterface } from 'node:readline';

const args = argv.slice(2);
let loginUrl = env.ERUN_REST_LOGIN_URL ?? '';
let sessionPath = env.ERUN_REST_SESSION ?? 'session.json';
for (let i = 0; i < args.length; i += 1) {
  if (args[i] === '--login') {
    loginUrl = args[(i += 1)] ?? '';
  } else if (args[i] === '--session') {
    sessionPath = args[(i += 1)] ?? sessionPath;
  }
}

if (!loginUrl) {
  console.error('error: a login URL is required (ERUN_REST_LOGIN_URL or --login)');
  exit(2);
}

const browser = await chromium.launch({ headless: false });
try {
  const context = await browser.newContext();
  const page = await context.newPage();
  await page.goto(loginUrl);
  console.error(`A browser window is open at ${loginUrl}.`);
  console.error('Log in there (including any MFA), then press Enter here to save the session.');
  await new Promise((resolve) => {
    const rl = createInterface({ input: stdin, output: stdout });
    rl.question('', () => {
      rl.close();
      resolve();
    });
  });
  await context.storageState({ path: sessionPath });
  console.error(`Saved session to ${sessionPath}. Treat it as a secret (it holds live cookies).`);
} finally {
  await browser.close();
}
