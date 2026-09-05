import { expect, test } from '@playwright/test';

// Real end-to-end proof for two of the console's plain-REST write surfaces
// that were, before this, only ever exercised against a mocked `fetch`:
// ProvisionPanel (src/provision/) and TenantsPanel (src/tenants/). Both ride
// the exact same same-origin httpBaseQuery transport `GET /v1/config` already
// proves live -- unlike the MCP JSON-RPC edge and the WebSocket attach edge,
// neither is a separate host with its own hand-rolled wire protocol, so
// neither carries the cross-origin/browser-security-policy risk class that
// actually broke those two (see mcp-operate-scope.spec.ts and
// mcp-attach-session.spec.ts). This suite exists to prove that structural
// argument rather than merely assert it: a mocked fetch cannot catch a
// schema drift between what the console's TypeScript types expect and what
// the real API actually returns on the wire.
const gated = process.env.ERUN_E2E_CONSOLE_REST !== '1';

test('provisioning a cloud context round-trips through the real API', async ({ page }) => {
  test.skip(
    gated,
    'opt-in: set ERUN_E2E_CONSOLE_REST=1 (./run-rest-surfaces.sh brings up the stack and sets it)',
  );

  await page.goto('/');
  await page
    .getByRole('navigation', { name: 'Console sections' })
    .getByRole('button', { name: 'Cloud contexts' })
    .click();

  await page.getByLabel('Alias name').fill('e2e-alias');
  await page
    .getByLabel(/BYO-cloud credentials JSON/)
    .fill('{"accessKeyId":"x","secretAccessKey":"y"}');
  await page.getByRole('button', { name: 'Save credentials' }).click();
  await expect(page.getByText('Credentials saved (encrypted server-side).')).toBeVisible();

  await page.getByLabel('Context name').fill('e2e-context');
  await page.getByLabel('Cloud provider alias').fill('e2e-alias');
  await page.getByLabel('Region').fill('eu-west-2');
  await page.getByRole('button', { name: 'Provision context' }).click();

  // No cloud provisioner is wired in this harness (registration-only, 201 —
  // see contexts.go's createContext), so this never reaches "running"; the
  // real proof is the create response coming back as a real, correctly
  // shaped CloudContext the console's own parser accepts and renders,
  // exactly the thing a mocked fetch cannot exercise.
  await expect(page.getByText('Provisioning e2e-context…')).toBeVisible();
});

test('registering a tenant round-trips through the real API', async ({ page }) => {
  test.skip(
    gated,
    'opt-in: set ERUN_E2E_CONSOLE_REST=1 (./run-rest-surfaces.sh brings up the stack and sets it)',
  );

  await page.goto('/');
  await page
    .getByRole('navigation', { name: 'Console sections' })
    .getByRole('button', { name: 'Tenants' })
    .click();

  await page.locator('#tenant-name').fill('e2ecreatedtenant');
  await page.locator('#tenant-issuer').fill('https://issuer.e2e-test.example');
  await page.getByRole('button', { name: 'Register tenant' }).click();

  await expect(page.getByText(/Registered e2ecreatedtenant\./)).toBeVisible();
  await expect(page.getByRole('cell', { name: 'e2ecreatedtenant' })).toBeVisible();
});
