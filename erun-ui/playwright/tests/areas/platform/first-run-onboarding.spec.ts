import type { Page } from '@playwright/test';

import { test, expect } from '../../../fixtures/erunApp.js';
import { AppShell } from '../../../pages/index.js';

// A genuinely fresh install has no tool config at all, so
// ResolveListResult fails with ErrNotInitialized instead of succeeding with
// zero tenants. LoadState() used to special-case that into a bare
// `{message: "...Run \`erun init\` first.", tenants: <unset>}` — a nil
// Tenants slice marshals to JSON `null`, and the frontend's boot sequence
// range-iterates it unconditionally before ever checking that message, so it
// threw and the sidebar/main-pane ErrorBoundary swallowed the whole subtree —
// hiding the "Initialize environment" affordance underneath it. Stub the
// exact historical wire shape to lock in that this now renders the same
// actionable empty state "zero tenants" gets, never a crash and never a bare
// CLI instruction.
async function stubFreshInstallState(page: Page): Promise<void> {
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string };
    if (body.method === 'LoadState') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: { tenants: null } }),
      });
    }
    await route.continue();
  });
}

test.describe('first-run onboarding (#1396)', () => {
  test('a genuinely fresh install renders the actionable empty state, never a crash or a bare CLI instruction', async ({
    page,
  }) => {
    await stubFreshInstallState(page);
    const app = new AppShell(page);
    await app.open();

    // Never the crash: the ErrorBoundary fallback that used to swallow the
    // whole sidebar/main-pane subtree.
    await expect(page.getByText('Something went wrong')).toHaveCount(0);

    // Always the actionable path: the same empty state "zero tenants" gets,
    // with its "Initialize environment" affordance reachable.
    const emptyState = page.getByText('No environments yet').locator('..');
    await expect(emptyState).toBeVisible();
    await expect(page.getByRole('button', { name: 'Initialize new environment' })).toBeVisible();

    // Never a bare instruction to run a CLI command.
    await expect(page.getByText(/erun init/i)).toHaveCount(0);
  });
});
