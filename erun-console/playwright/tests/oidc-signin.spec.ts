import { expect, test } from '@playwright/test';

// The credentials of the first-instance admin the Zitadel harness provisions
// (see ../zitadel/provision.sh). Overridable so the spec can target a
// differently-seeded instance.
const loginName = process.env.E2E_LOGIN_NAME ?? 'zadmin@erun.localhost';
const password = process.env.E2E_LOGIN_PASSWORD ?? 'Password1!';

// Full browser sign-in against a real Zitadel v4 (core + Login V2). This is the
// end-to-end proof for #606/#684 that the unit tests (src/auth/auth.test.ts,
// mocked discovery/token) cannot give: the console redirects to the issuer, the
// operator authenticates in Zitadel's own Login V2 UI, the PKCE code exchange
// yields an id_token, and the API verifies it against Zitadel's JWKS and (on an
// empty database) bootstraps the OPERATIONS tenant, which GET /v1/config renders.
test('operator signs in through Zitadel OIDC and sees their tenant config', async ({ page }) => {
  // 1. Signed-out console offers the real OIDC "Sign in" (not the dev fallback).
  await page.goto('/');
  await expect(page.getByText('Sign in to view your environments.')).toBeVisible();
  await page.getByRole('button', { name: 'Sign in' }).click();

  // 2. Redirected to Zitadel's Login V2 — loginname step.
  await expect(page).toHaveURL(/\/ui\/v2\/login\/loginname/);
  await page.getByTestId('username-text-input').fill(loginName);
  await page.getByTestId('submit-button').click();

  // 3. Password step (no forced password change — the harness sets
  //    PASSWORDCHANGEREQUIRED=false on the first-instance admin).
  await expect(page).toHaveURL(/\/ui\/v2\/login\/password/);
  await page.getByTestId('password-text-input').fill(password);
  await page.getByTestId('submit-button').click();

  // 4. Back at the console: the callback exchange + JWKS-verified id_token
  //    resolve the tenant and GET /v1/config renders it. The first-ever sign-in
  //    bootstraps OPERATIONS; subsequent runs resolve the same tenant.
  await expect(page.getByRole('heading', { name: /operations/i })).toBeVisible();
  await expect(page.getByText('Tenant · OPERATIONS')).toBeVisible();
});
