import { expect, test } from '@playwright/test';

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
  // Sign in renders only if discovery reached the API through the /v1 proxy.
  await expect(page.getByRole('button', { name: 'Sign in' })).toBeVisible();
});
