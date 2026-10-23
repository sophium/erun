import { expect, type Page } from '@playwright/test';

// The deployment's e2e operator: an existing, password-only account in the
// deployment's own IdP, supplied as BuildKit secrets (see the Dockerfile).
// The console keeps its token in sessionStorage, which a Playwright
// storageState does not carry, so each test signs in through the real flow.
export const credentials = {
  loginName: process.env.E2E_LOGIN_NAME ?? '',
  password: process.env.E2E_LOGIN_PASSWORD ?? '',
};

export const missingCredentials =
  credentials.loginName === '' || credentials.password === ''
    ? 'needs the e2e operator account: the e2e_login_name and e2e_login_password secrets (.erun/config.yaml e2e.secrets)'
    : '';

// The console's own origin, as the browser lands back on it after the IdP's
// redirect: the signed-in shell and the signed-out landing both live at `/`.
export function consoleRoot(baseURL: string | undefined): string {
  if (baseURL === undefined) {
    throw new Error('the Playwright config sets baseURL from ERUN_E2E_BASE_URL');
  }
  return new URL('/', baseURL).href;
}

// Drives the console's OIDC Authorization Code + PKCE sign-in through the
// deployment's Zitadel Login V2 UI and waits for the signed-in shell.
export async function signIn(page: Page, root: string): Promise<void> {
  await page.goto('/');
  await page.getByRole('button', { name: 'Sign in' }).click();

  await expect(page).toHaveURL(/\/ui\/v2\/login\/loginname/);
  await page.getByTestId('username-text-input').fill(credentials.loginName);
  await page.getByTestId('submit-button').click();

  await expect(page).toHaveURL(/\/ui\/v2\/login\/password/);
  await page.getByTestId('password-text-input').fill(credentials.password);
  await page.getByTestId('submit-button').click();

  await expect(page).toHaveURL(root);
  await expect(page.getByRole('navigation', { name: 'Console sections' })).toBeVisible();
}
