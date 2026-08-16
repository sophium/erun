import { expect, test } from '@playwright/test';

// Opt-in, like the backend's live env-deploy gate: this spec needs a real
// Zitadel, a migrated API and the console dev server, all of which run.sh stands
// up and sets ERUN_E2E_CONSOLE_OIDC for. A suite that silently assumes a running
// IdP is worse than no suite, so absent the gate it skips rather than fails.
const gated = process.env.ERUN_E2E_CONSOLE_OIDC !== '1';

// Provisioned by ../zitadel/provision.sh into ../.e2e-oidc.env, which run.sh
// sources. Defaults keep the spec readable, not runnable on its own.
const loginName = process.env.E2E_LOGIN_NAME ?? 'erun-e2e-operator';
const password = process.env.E2E_LOGIN_PASSWORD ?? 'E2eOperator1!';

// Full browser sign-in against a real Zitadel v4 (core + Login V2). This is the
// end-to-end proof the unit tests (src/auth/auth.test.ts, mocked discovery and
// token endpoints) cannot give: the console redirects to the issuer, the
// operator authenticates in Zitadel's own login UI, the PKCE code exchange
// yields an id_token, and the API verifies that token against Zitadel's live
// JWKS and — on an empty database — bootstraps the operations tenant, which
// GET /v1/config then renders.
test('operator signs in through Zitadel OIDC and sees their tenant config', async ({ page }) => {
  test.skip(
    gated,
    'opt-in: set ERUN_E2E_CONSOLE_OIDC=1 (./run.sh brings up the stack and sets it)',
  );

  // 1. The signed-out console offers the real OIDC sign-in, not the dev-token
  //    fallback: a Sign in button only renders when the issuer is configured.
  await page.goto('/');
  await expect(page.getByText('Sign in to view your environments.')).toBeVisible();
  await page.getByRole('button', { name: 'Sign in' }).click();

  // 2. The browser lands on Zitadel's own Login V2 UI — loginname step.
  await expect(page).toHaveURL(/\/ui\/v2\/login\/loginname/);
  await page.getByTestId('username-text-input').fill(loginName);
  await page.getByTestId('submit-button').click();

  // 3. Password step. The provisioned user has passwordChangeRequired cleared
  //    and the org's login policy allows neither passkeys nor MFA, so this is
  //    the last page before the redirect back.
  await expect(page).toHaveURL(/\/ui\/v2\/login\/password/);
  await page.getByTestId('password-text-input').fill(password);
  await page.getByTestId('submit-button').click();

  // 4. Back at the console, signed in: the callback exchange produced a
  //    JWKS-verified id_token, and GET /v1/config rendered the tenant it
  //    resolves to. Asserting the rendered read model — not merely that a
  //    redirect happened — is what makes this an authenticated-session proof.
  await expect(page).toHaveURL(/^http:\/\/localhost:5173\/$/);
  await expect(page.getByRole('heading', { name: /operations/i })).toBeVisible();
  await expect(page.getByText('Tenant · OPERATIONS')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Environments' })).toBeVisible();

  // 5. The session is real, not a one-shot render: a reload resolves the held
  //    token and re-authenticates against the API without another sign-in.
  await page.reload();
  await expect(page.getByText('Tenant · OPERATIONS')).toBeVisible();
});
