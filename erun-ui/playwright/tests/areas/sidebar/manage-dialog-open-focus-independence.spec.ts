import type { Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// Opening the manage dialog must not depend on the row's edit button holding
// focus.
//
// openManageDialogViaKeyboard drove that button by key — focus it, press
// Enter — because a mouse press hovers the row first and the IconTooltip
// popper that opens then swallows the click (see its own comment). That
// settled the pointer race and left a focus race in its place: a
// boot-reattached environment restores its terminal, which takes focus
// asynchronously, and the keydown then lands on whatever took it. The step
// re-focuses on every retry, so a thief that keeps winning does not fail
// fast — it spends the calling test's whole budget and reds whichever spec
// happened to call it (erun#2687: three gate runs, three different specs).
//
// The case below makes that dependency deterministic rather than
// load-dependent: a capture-phase focusin listener drops focus straight back
// out of the sidebar, so the edit button can never hold it. Pre-fix the
// keydown never reaches the button and the step reds at the test's own
// deadline; post-fix the step activates the button without focusing anything
// and converges. This is the reproduction, not a slowdown — a merely slow
// render is covered separately, by sidebar-pom-convergence-budget.spec.ts.
//
// Scoped to the sidebar rather than the whole document: the manage dialog is
// a portal at document.body whose own focus trap (and focus-outside
// handling) is not what this models and must be left alone.
async function takeSidebarFocusAway(page: Page): Promise<void> {
  await page.evaluate(() => {
    const sidebar = document.querySelector('aside');
    const steal = (event: Event) => {
      const target = event.target;
      if (!(target instanceof HTMLElement) || !sidebar?.contains(target)) return;
      // blur() returns focus to the document body — the same place a keydown
      // ends up when the restored terminal has taken it.
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
