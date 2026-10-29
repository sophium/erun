import type { Page } from '@playwright/test';

import { test, expect, withTestBudget } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

test.describe('sidebar', () => {
  test('tenant toggle flips aria-expanded', async ({ app }) => {
    const before = await app.sidebar.isTenantExpanded(SEED_TENANT);
    await app.sidebar.toggleTenant(SEED_TENANT);
    // isTenantExpanded is a bare read, so poll it past the re-render.
    await expect.poll(() => app.sidebar.isTenantExpanded(SEED_TENANT)).toBe(!before);
    // Restore so the state does not leak into the next test in this worker.
    await app.sidebar.toggleTenant(SEED_TENANT);
    await expect.poll(() => app.sidebar.isTenantExpanded(SEED_TENANT)).toBe(before);
  });

  test('tenant row carries a visible affordance for opening the dashboard', async ({ app }) => {
    const icon = app.sidebar.tenantDashboardButton(SEED_TENANT).locator('svg');
    await expect(icon).toBeVisible();
    await expect(icon).toHaveAttribute('aria-hidden', 'true');
  });

  test('opening an environment surfaces status feedback', async ({ app, page }) => {
    // The opening status lasts a few hundred ms, so a locator would race it;
    // an observer installed before the click records it instead.
    const target = `${SEED_TENANT} / ${SEED_ENV_ALPHA}`;
    const openingStatus = await recordOpeningStatus(page, target);
    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);
    // expect.poll defaults to expect.timeout, not the test deadline.
    await expect.poll(openingStatus, withTestBudget()).not.toBeNull();
  });
});

// recordOpeningStatus records the transient opening status from a
// MutationObserver installed before the click.
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
    // Keep only the first, bounded match: an ancestor match would include the
    // whole terminal scrollback.
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
