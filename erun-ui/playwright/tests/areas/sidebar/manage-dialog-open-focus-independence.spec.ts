import type { Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// Opening the manage dialog must not depend on the edit button holding focus, since a restored
// terminal can take it asynchronously.
// Scoped to the sidebar so the dialog portal's own focus trap is left alone.
async function takeSidebarFocusAway(page: Page): Promise<void> {
  await page.evaluate(() => {
    const sidebar = document.querySelector('aside');
    const steal = (event: Event) => {
      const target = event.target;
      if (!(target instanceof HTMLElement) || !sidebar?.contains(target)) return;
      target.blur();
    };
    document.addEventListener('focusin', steal, true);
    (window as unknown as { __erunFocusThief?: () => void }).__erunFocusThief = () =>
      document.removeEventListener('focusin', steal, true);
  });
}

async function restoreFocus(page: Page): Promise<void> {
  await page.evaluate(() => {
    const w = window as unknown as { __erunFocusThief?: () => void };
    w.__erunFocusThief?.();
    delete w.__erunFocusThief;
  });
}

test('a manage dialog opens from a sidebar row that cannot keep focus', async ({ app, page }) => {
  await takeSidebarFocusAway(page);
  try {
    await app.sidebar.openManageDialogFor(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await expect(app.manageDialog.tab('General')).toBeVisible();
  } finally {
    await restoreFocus(page);
  }
});
