import type { Page } from '@playwright/test';

import { test, expect, withTestBudget } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

test.describe('sidebar', () => {
  test('tenant toggle flips aria-expanded', async ({ app }) => {
    const before = await app.sidebar.isTenantExpanded(SEED_TENANT);
    await app.sidebar.toggleTenant(SEED_TENANT);
    // isTenantExpanded is a bare attribute read, not an auto-retrying
    // assertion, so it must not run right after the click: the toggle's
    // re-render is a separate step under React, and a read that lands before
    // it lands would report the pre-click value under contention.
    await expect.poll(() => app.sidebar.isTenantExpanded(SEED_TENANT)).toBe(!before);
    // Restore state so subsequent assertions in the suite don't drift --
    // converge on the restore too, so a slow one can't leak into the next
    // test in this worker.
    await app.sidebar.toggleTenant(SEED_TENANT);
    await expect.poll(() => app.sidebar.isTenantExpanded(SEED_TENANT)).toBe(before);
  });

  // The tenant row's only prior affordance was the folder-group header
  // (#1218): nothing indicated the name itself opens a dashboard. It now
  // carries a visible icon alongside the name.
  test('tenant row carries a visible affordance for opening the dashboard', async ({ app }) => {
    const icon = app.sidebar.tenantDashboardButton(SEED_TENANT).locator('svg');
    await expect(icon).toBeVisible();
    await expect(icon).toHaveAttribute('aria-hidden', 'true');
  });

  test('opening an environment surfaces status feedback', async ({ app, page }) => {
    // The opening status is inherently transient — it lives only for the open
    // itself (a few hundred ms): the "Opening …" message clears the instant the
    // session's first output lands, and the sidebar row's busy spinner
    // (role=status, its aria-label naming the env) clears when the open settles.
    // Match either surface, and match the spinner by the env name because its
    // wording varies ("Opening …" before the open action starts running,
    // "Working on …" once it is).
    //
    // Asserting it with a locator races that window and loses on a fast open:
    // measured on one commit, green under contention and "element(s) not found"
    // after the full timeout when run alone (#2577). Recording it from inside
    // the page — with the observer installed BEFORE the click — closes the
    // window. No wall-clock wait is added; what changes is only that a
    // short-lived render can no longer fall between two polls.
    const target = `${SEED_TENANT} / ${SEED_ENV_ALPHA}`;
    const openingStatus = await recordOpeningStatus(page, target);
    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);
    // `expect.poll` does not resolve to the enclosing test's deadline the way
    // waitFor/toPass do -- with no timeout it lands on `expect.timeout`'s
    // separate 10s clock -- so it carries this test's declared budget instead
    // of a cap picked here (fixtures/erunApp.ts withTestBudget).
    await expect.poll(openingStatus, withTestBudget()).not.toBeNull();
  });
});

// recordOpeningStatus installs an in-page recorder for the transient status an
// environment shows while it opens, and returns a reader for whatever it saw.
//
// It exists because that status is short-lived by design (#2577): a locator
// assertion has to catch it inside its own render window, and on a fast open
// that window can close between two polls. A MutationObserver installed before
// the click cannot miss it, so the assertion still reads real rendered DOM —
// it just can no longer be raced out of observing it.
async function recordOpeningStatus(
  page: Page,
  target: string,
): Promise<() => Promise<string | null>> {
  await page.evaluate((wanted) => {
    const scope = window as unknown as {
      __erunOpeningStatus?: string | null;
      __erunOpeningObserver?: MutationObserver;
    };
    scope.__erunOpeningObserver?.disconnect();
    scope.__erunOpeningStatus = null;
    const label = (el: Element): string => el.getAttribute('aria-label') ?? el.textContent ?? '';
    const matches = (el: Element): boolean =>
      (el.getAttribute('role') === 'status' && label(el).includes(wanted)) ||
      (el.textContent ?? '').includes(`Opening ${wanted}`);
    // Only the first match is kept, and only bounded text: a match found on an
    // ancestor reports that ancestor's whole text, which for the terminal pane
    // would be the entire scrollback.
    const record = (el: Element): void => {
      if (scope.__erunOpeningStatus === null && matches(el)) {
        scope.__erunOpeningStatus = label(el).slice(0, 200);
      }
    };
    const scan = (node: Node): void => {
      if (scope.__erunOpeningStatus !== null) return;
      if (!(node instanceof Element)) {
        if (node.parentElement) record(node.parentElement);
        return;
      }
      record(node);
      for (const child of Array.from(node.querySelectorAll('*'))) {
        if (scope.__erunOpeningStatus !== null) return;
        record(child);
      }
    };
    scope.__erunOpeningObserver = new MutationObserver((records) => {
      for (const mutation of records) {
        if (mutation.type === 'childList') {
          for (const added of Array.from(mutation.addedNodes)) scan(added);
        } else if (mutation.target instanceof Element) {
          record(mutation.target);
        } else if (mutation.target.parentElement) {
          record(mutation.target.parentElement);
        }
      }
    });
    scope.__erunOpeningObserver.observe(document.body, {
      childList: true,
      subtree: true,
      attributes: true,
      characterData: true,
    });
  }, target);
  return () =>
    page.evaluate(
      () =>
        (window as unknown as { __erunOpeningStatus?: string | null }).__erunOpeningStatus ?? null,
    );
}
