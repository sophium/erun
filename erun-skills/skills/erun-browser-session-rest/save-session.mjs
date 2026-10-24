#!/usr/bin/env node
// Saves an interactive browser login as Playwright storageState. Login is manual
// by design and no password is read; see SKILL.md.
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
