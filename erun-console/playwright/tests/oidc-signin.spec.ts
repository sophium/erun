import { expect, test } from '@playwright/test';

// Registering an environment and hitting the real deploy-executor-unconfigured
// response is appended to THIS test (one sign-in) rather than living in its
// own spec/test that signs in again as the same Zitadel user: a second fresh
// login for the same username, run immediately after the first in this
// harness, left the Login V2 loginname step's Continue button permanently
// disabled (the typed username never registered). Reusing the one
// already-authenticated session sidesteps that and is also simply less
// wasteful of the heaviest fixture this suite stands up.

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
test('operator signs in through Zitadel OIDC, registers an environment, and sees the deploy-executor-unconfigured response', async ({ page }) => {
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
  // exact: true — the "Register and deploy environments" panel's own heading
  // (below) also contains "environments" as a substring, which a non-exact
  // match would ambiguously match too.
  await expect(page.getByRole('heading', { name: 'Environments', exact: true })).toBeVisible();

  // 5. The session is real, not a one-shot render: a reload resolves the held
  //    token and re-authenticates against the API without another sign-in.
  await page.reload();
  await expect(page.getByText('Tenant · OPERATIONS')).toBeVisible();

  // 6. Register an environment against the real API (not a mock) and confirm
  //    it appears in the read view. `getByLabel('Name', { exact: true })`
  //    disambiguates from the alias/context forms' "Alias name"/"Context name"
  //    fields below, which a substring match on "Name" would also match.
  const envName = `e2e-${Date.now()}`;
  await page.getByLabel('Name', { exact: true }).fill(envName);
  await page.getByRole('button', { name: 'Register environment' }).click();
  await expect(page.getByText(`Environment ${envName} registered.`)).toBeVisible();
  await expect(page.getByRole('cell', { name: envName })).toBeVisible();

  // 7. Deploying it hits the real control plane's honest answer when no
  //    deploy executor is configured (run.sh's eapi sets no
  //    ERUN_ENV_DEPLOYER_SERVICE_ACCOUNT) — a real 501 rendered as the plain,
  //    actionable message, not a generic failure. Standing up a real deploy
  //    executor (a cluster, a runtime image registry) is out of scope for this
  //    e2e; the 409/501/success paths are unit-tested against a mocked fetch
  //    in EnvironmentsPanel.test.tsx.
  const deployRow = page.locator('.deploy-row', { hasText: envName });
  await deployRow.getByRole('button', { name: 'Deploy' }).click();
  await expect(
    page.getByText('The deploy executor is not configured on this control plane.'),
  ).toBeVisible();
});
