import { expect, test } from '@playwright/test';

import { consoleRoot, missingCredentials, signIn } from './signIn';

const attachEnvironment = process.env.E2E_ATTACH_ENVIRONMENT ?? '';

// Fixed so repeated runs re-attach instead of leaving shells behind.
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

  await page.getByRole('button', { name: 'Generate MCP token' }).click();
  await expect(
    page.getByText(new RegExp(`erun-mcp:[^/\\s]+/${escapeRegExp(attachEnvironment)}`)),
  ).toBeVisible();

  await expect(page.getByLabel(/Environment edge hostname/)).not.toHaveValue('');
  await page.getByLabel(/Session id/).fill(sessionId);
  await page.getByRole('button', { name: 'Attach' }).click();
  await expect(page.getByRole('button', { name: 'Disconnect' })).toBeVisible();

  // Only the evaluated output, not the echoed input, contains the expected text.
  const run = Date.now().toString();
  await page.getByLabel('Send a line to the session').fill(`echo ATTACH_E2E_$((6*7))_${run}`);
  await page.getByRole('button', { name: 'Send' }).click();
  await expect(page.getByRole('log', { name: 'Attach session output' })).toContainText(
    `ATTACH_E2E_42_${run}`,
  );

  await page.getByRole('button', { name: 'Disconnect' }).click();
  await expect(page.getByRole('button', { name: 'Attach' })).toBeVisible();
});
