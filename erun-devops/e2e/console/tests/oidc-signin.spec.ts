import { expect, test } from '@playwright/test';

import { consoleRoot, missingCredentials, signIn } from './signIn';

test('an operator signs in through the IdP, keeps the session across a reload, and signing out ends it', async ({
  page,
  baseURL,
}) => {
  test.skip(missingCredentials !== '', missingCredentials);
  const root = consoleRoot(baseURL);

  await signIn(page, root);

  await expect(page.getByRole('heading', { level: 2 })).toBeVisible();
  await expect(page.getByRole('main').getByText(/^Tenant · /)).toBeVisible();

  await page.reload();
  await expect(page.getByRole('main').getByText(/^Tenant · /)).toBeVisible();

  await page.getByRole('button', { name: 'Sign out' }).click();
  await expect(page).toHaveURL(root);
  await expect(page.getByRole('button', { name: 'Sign in' })).toBeVisible();

  // Without RP-initiated logout this would skip the login prompt.
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page).toHaveURL(/\/ui\/v2\/login\/loginname/);
});
