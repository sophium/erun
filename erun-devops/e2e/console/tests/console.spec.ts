import { expect, test } from '@playwright/test';

// What every deployment answers without an account: the build actually
// serving it, over a certificate the browser and Node both verify, and a
// signed-out front door wired to a real IdP.
const deployedVersion = process.env.ERUN_E2E_VERSION ?? '';

test('the console is served over https', ({ baseURL }) => {
  expect(baseURL, 'ERUN_E2E_BASE_URL').toMatch(/^https:\/\//);
});

test('the console serves the deployed version', async ({ request }) => {
  expect(deployedVersion, 'ERUN_E2E_VERSION is required').not.toBe('');

  const response = await request.get('/version.json');
  expect(response.status()).toBe(200);
  expect(await response.json()).toEqual({ version: deployedVersion });
});

test('the signed-out landing page offers OIDC sign-in', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
  // Sign in renders only when platform discovery returned an issuer, so this
  // also proves the same-origin /v1 proxy reaches the deployment's API.
  await expect(page.getByRole('button', { name: 'Sign in' })).toBeVisible();
});
