import { expect, test } from '@playwright/test';

import { consoleRoot, missingCredentials, signIn } from './signIn';

// Full browser sign-in against the deployment's own IdP: the console
// redirects to the issuer, the operator authenticates in its Login V2 UI, the
// PKCE code exchange yields an id_token, and the API verifies it against the
// issuer's live JWKS before GET /v1/config renders the tenant it resolves to.
// The unit tests mock discovery and the token endpoint; this is the proof
// they cannot give, through the deployed ingress and certificate.
test('an operator signs in through the IdP, keeps the session across a reload, and signing out ends it', async ({
  page,
  baseURL,
}) => {
  test.skip(missingCredentials !== '', missingCredentials);
  const root = consoleRoot(baseURL);

  await signIn(page, root);

  // The rendered read model, not merely a redirect, is what makes this an
  // authenticated session. The tenant's name and type belong to the
  // deployment, so they are asserted structurally.
  await expect(page.getByRole('heading', { level: 2 })).toBeVisible();
  await expect(page.getByRole('main').getByText(/^Tenant · /)).toBeVisible();

  // The held token re-authenticates against the API without another sign-in.
  await page.reload();
  await expect(page.getByRole('main').getByText(/^Tenant · /)).toBeVisible();

  // Signing out redirects through the IdP's end_session_endpoint and back.
  await page.getByRole('button', { name: 'Sign out' }).click();
  await expect(page).toHaveURL(root);
  await expect(page.getByRole('button', { name: 'Sign in' })).toBeVisible();

  // Had RP-initiated logout not ended the IdP session, signing in again would
  // skip straight back into the console with no login prompt.
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page).toHaveURL(/\/ui\/v2\/login\/loginname/);
});
