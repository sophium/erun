import type { Locator, Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_ORCHESTRATOR, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// HoverCardRow's 12px dt and 14px dd must share a baseline; asserted geometrically, not by class
// name.

interface RowGeometry {
  dtBottom: number;
  ddTop: number;
  dtBaseline: number;
  ddBaseline: number;
}

// Measures each baseline via a zero-size marker in the first blockified child, so stacked values
// use their first line.
async function rowGeometry(card: Locator, label: string): Promise<RowGeometry> {
  return card.evaluate(
    (root, label) => {
      function contentAnchor(start: Element): Element {
        let el = start;
        for (let i = 0; i < 5; i += 1) {
          const kids = Array.from(el.childNodes).filter(
            (node) => !(node.nodeType === Node.TEXT_NODE && !node.textContent?.trim()),
          );
          const first = kids[0];
          if (!first || first.nodeType !== Node.ELEMENT_NODE) {
            break;
          }
          const display = window.getComputedStyle(first as Element).display;
          if (display !== 'block' && display !== 'grid' && display !== 'flex') {
            break;
          }
          el = first as Element;
        }
        return el;
      }
      function baselineTop(el: Element): number {
        const anchor = contentAnchor(el);
        const marker = document.createElement('span');
        marker.style.cssText = 'display:inline-block;width:0;height:0;vertical-align:baseline';
        anchor.appendChild(marker);
        const top = marker.getBoundingClientRect().top;
        marker.remove();
        return top;
      }
      const dt = Array.from(root.querySelectorAll('dt')).find(
        (element) => element.textContent?.trim() === label,
      );
      if (!dt) {
        throw new Error(`no dt labeled "${label}"`);
      }
      const dd = dt.nextElementSibling;
      if (!dd || dd.tagName !== 'DD') {
        throw new Error(`dt labeled "${label}" has no dd sibling`);
      }
      return {
        dtBottom: dt.getBoundingClientRect().bottom,
        ddTop: dd.getBoundingClientRect().top,
        dtBaseline: baselineTop(dt),
        ddBaseline: baselineTop(dd),
      };
    },
    label,
    { timeout: 1_000 },
  );
}

const RUNNING_SESSION_ID = 4242;

function orchestratorSnapshot(overrides: Record<string, unknown>): Record<string, unknown> {
  return {
    id: SEED_ORCHESTRATOR,
    name: SEED_ORCHESTRATOR,
    environments: [],
    tenants: [],
    directories: [],
    sessionId: RUNNING_SESSION_ID,
    status: 'running',
    busy: false,
    busyAtUnix: 0,
    transient: false,
    shellRunning: false,
    shellCommand: '',
    shellStartedAtUnix: 0,
    ...overrides,
  };
}

async function stubOrchestratorList(page: Page, body: Record<string, unknown>): Promise<void> {
  await page.route('**/__erun_invoke', async (route, request) => {
    const parsed = JSON.parse(request.postData() ?? '{}') as { method?: string };
    if (parsed.method === 'ListOrchestrators') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: [body] }),
      });
    }
    await route.continue();
  });
}

test.describe('sidebar hover card baseline alignment (#1759)', () => {
  test('the environment card aligns a single-line label and value to one baseline', async ({
    app,
  }) => {
    // Two round-trips can approach the 30s default; the retry below is uncapped so this budget
    // applies.
    test.setTimeout(60_000);
    await app.reboot();
    // The default-landing env's auto-open re-renders the row and drops the card; wait for it to
    // finish first.
    await app.page
      .getByRole('button', { name: `Opening ${SEED_TENANT} / ${SEED_ENV_ALPHA}...` })
      .waitFor({ state: 'hidden' });
    const card = app.sidebar.envHoverCard(SEED_TENANT, SEED_ENV_ALPHA);
    // The card's open state is the row's React state, so hover and measure in one retryable block.
    await expect(async () => {
      await app.sidebar.hoverEnvironmentRow(SEED_TENANT, SEED_ENV_ALPHA);
      await expect(card).toBeVisible({ timeout: 1_000 });
      await expect(card).toContainText('Idle', { timeout: 1_000 });
      const geometry = await rowGeometry(card, 'Activity');
      expect(Math.abs(geometry.dtBaseline - geometry.ddBaseline)).toBeLessThan(1.5);
    }).toPass();
  });

  test('the environment card no longer stacks the Version row now that `wide` is retired (#1901)', async ({
    app,
  }) => {
    // See the preceding test.
    test.setTimeout(60_000);
    await app.reboot();
    await app.page
      .getByRole('button', { name: `Opening ${SEED_TENANT} / ${SEED_ENV_ALPHA}...` })
      .waitFor({ state: 'hidden' });
    const card = app.sidebar.envHoverCard(SEED_TENANT, SEED_ENV_ALPHA);
    // Uncapped so the test's own budget applies.
    await expect(async () => {
      await app.sidebar.hoverEnvironmentRow(SEED_TENANT, SEED_ENV_ALPHA);
      await expect(card).toBeVisible({ timeout: 1_000 });
      // seedEnvironment pins runtimeversion to 1.0.0.
      await expect(card).toContainText('1.0.0', { timeout: 1_000 });

      const geometry = await rowGeometry(card, 'Version');
      expect(geometry.dtBottom).toBeGreaterThan(geometry.ddTop - 1);
      expect(Math.abs(geometry.dtBaseline - geometry.ddBaseline)).toBeLessThan(1.5);
    }).toPass();
  });

  test('the orchestrator card aligns a single-line label and value to one baseline', async ({
    app,
    page,
  }) => {
    await stubOrchestratorList(
      page,
      orchestratorSnapshot({ busy: true, busyAtUnix: Math.floor(Date.now() / 1000) - 60 }),
    );
    await app.reboot();

    let geometry: RowGeometry | undefined;
    await app.sidebar.readOrchestratorHoverCard(SEED_ORCHESTRATOR, async (card) => {
      await expect(card).toBeVisible({ timeout: 1_000 });
      await expect(card).toContainText('Working, for', { timeout: 1_000 });
      geometry = await rowGeometry(card, 'Doing');
    });
    expect(Math.abs(geometry!.dtBaseline - geometry!.ddBaseline)).toBeLessThan(1.5);
  });

  test('the orchestrator card aligns a stacked, multi-line value to the label baseline', async ({
    app,
    page,
  }) => {
    await stubOrchestratorList(
      page,
      orchestratorSnapshot({
        busy: true,
        busyAtUnix: Math.floor(Date.now() / 1000) - 60,
        shellRunning: true,
        shellCommand: 'gradle build',
        shellStartedAtUnix: Math.floor(Date.now() / 1000) - 30,
      }),
    );
    await app.reboot();

    let geometry: RowGeometry | undefined;
    await app.sidebar.readOrchestratorHoverCard(SEED_ORCHESTRATOR, async (card) => {
      await expect(card).toBeVisible({ timeout: 1_000 });
      await expect(card).toContainText('Working, for', { timeout: 1_000 });
      await expect(card).toContainText('Shell running', { timeout: 1_000 });

      // Doing stacks two lines; alignment applies to the first.
      geometry = await rowGeometry(card, 'Doing');
    });
    expect(Math.abs(geometry!.dtBaseline - geometry!.ddBaseline)).toBeLessThan(1.5);
  });

  test('the orchestrator card keeps the wide Environments row stacked, not baseline-shoved', async ({
    app,
    page,
  }) => {
    await stubOrchestratorList(
      page,
      orchestratorSnapshot({
        environments: [{ tenant: SEED_TENANT, environment: SEED_ENV_ALPHA, directory: '/tmp/a' }],
      }),
    );
    await app.reboot();

    let geometry: RowGeometry | undefined;
    await app.sidebar.readOrchestratorHoverCard(SEED_ORCHESTRATOR, async (card) => {
      await expect(card).toBeVisible({ timeout: 1_000 });
      await expect(card).toContainText(SEED_ENV_ALPHA, { timeout: 1_000 });

      // The wide variant puts dt and dd in separate grid rows.
      geometry = await rowGeometry(card, 'Environments');
    });
    expect(geometry!.dtBottom).toBeLessThanOrEqual(geometry!.ddTop + 1);
  });
});
