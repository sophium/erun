import { expect, test } from '@playwright/test';

import { consoleRoot, missingCredentials, signIn } from './signIn';

// A console session attaches to a live environment over its exposed MCP
// edge: a real browser WebSocket at a different origin than the console,
// over wss:// with the edge's real certificate, carrying the
// subprotocol bearer token into a real dtach/PTY. The component tests only
// ever exercise this against a mocked WebSocket.
//
// The environment is the deployment's own: one whose MCP edge is exposed and
// that the e2e operator may attach to, named by the e2e_attach_environment
// secret.
const attachEnvironment = process.env.E2E_ATTACH_ENVIRONMENT ?? '';

// One fixed session id, so repeated runs re-attach to the same dtach session
// rather than leaving a new shell behind in the environment every time.
const sessionId = 'erun-e2e-attach';

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

test('an operator attaches to a live session over the exposed edge and drives a real shell', async ({
  page,
  baseURL,
}) => {
  test.skip(missingCredentials !== '', missingCredentials);
  test.skip(
    attachEnvironment === '',
    'needs an environment with an exposed MCP edge: the e2e_attach_environment secret (.erun/config.yaml e2e.secrets)',
  );

  await signIn(page, consoleRoot(baseURL));
  await page
    .getByRole('navigation', { name: 'Console sections' })
    .getByRole('button', { name: 'MCP access' })
    .click();
  await page.getByRole('combobox', { name: 'Environment' }).click();
  await page.getByRole('option', { name: attachEnvironment }).click();

  // The attach form mints its own erun:attach token; any scope renders it.
  await page.getByRole('button', { name: 'Generate MCP token' }).click();
  await expect(
    page.getByText(new RegExp(`erun-mcp:[^/\\s]+/${escapeRegExp(attachEnvironment)}`)),
  ).toBeVisible();

  // An exposed environment's edge hostname is discovered, not typed.
  await expect(page.getByLabel(/Environment edge hostname/)).not.toHaveValue('');
  await page.getByLabel(/Session id/).fill(sessionId);
  await page.getByRole('button', { name: 'Attach' }).click();
  await expect(page.getByRole('button', { name: 'Disconnect' })).toBeVisible();

  // The shell evaluates the arithmetic, so the expected text appears only in
  // the PTY's output, never in the echo of the line that was sent.
  const run = Date.now().toString();
  await page.getByLabel('Send a line to the session').fill(`echo ATTACH_E2E_$((6*7))_${run}`);
  await page.getByRole('button', { name: 'Send' }).click();
  await expect(page.getByRole('log', { name: 'Attach session output' })).toContainText(
    `ATTACH_E2E_42_${run}`,
  );

  await page.getByRole('button', { name: 'Disconnect' }).click();
  await expect(page.getByRole('button', { name: 'Attach' })).toBeVisible();
});
