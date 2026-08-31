import type { Locator, Page } from '@playwright/test';

import { expect, test } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_ORCHESTRATOR, SEED_TENANT } from '../fixtures/seedRoot.js';

// erun#1759: HoverCardRow (Sidebar.HoverCardRow.tsx) renders a 12px `dt` and
// a 14px `dd` as siblings in a grid row, and neither sidebar hover card's
// `dl` set `items-baseline` -- CSS grid's default `align-items: stretch`
// let each cell fill the row, so each line sat at the top of its own cell
// and the two font sizes' text baselines landed a couple of pixels apart in
// every row of both cards.
//
// This asserts the fix geometrically -- measured text baselines actually
// coincide -- rather than checking the `items-baseline` class string is
// present anywhere, mirroring titlebar-whip-panel-layout.spec.ts's own
// boundingBox()-based layout assertion for the same reason: a class on the
// wrong element, or one overridden later in the cascade, would still pass a
// string check.

interface RowGeometry {
  dtBottom: number;
  ddTop: number;
  dtBaseline: number;
  ddBaseline: number;
}

// rowGeometry finds the row labeled `label` inside `card` and measures both
// its `dt` and `dd`. The baseline of each is found by walking into the
// first blockified child the way the browser's own "baseline of a box"
// algorithm does -- a `<span>` becomes a block-level box the instant it is a
// grid/flex item, which is exactly what HoverCardRow's stacked multi-line
// values (Usage, Doing) do -- so a stacked value is measured at its first
// line's baseline, not its own bottom edge. A zero-size, baseline-aligned
// marker appended into that anchor lands its own top edge exactly on the
// line's baseline; this is a standard DOM technique, not an approximation
// from font metrics.
async function rowGeometry(card: Locator, label: string): Promise<RowGeometry> {
  return card.evaluate((root, label) => {
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
  }, label);
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
    await app.reboot();
    await app.sidebar.hoverEnvironmentRow(SEED_TENANT, SEED_ENV_ALPHA);
    const card = app.sidebar.envHoverCard(SEED_TENANT, SEED_ENV_ALPHA);
    await expect(card).toBeVisible();
    // seedEnvironment pins every seeded env's runtimeversion to 1.0.0, so the
    // Version row always renders the version, never the "Not set" empty case.
    await expect(card).toContainText('1.0.0');

    const geometry = await rowGeometry(card, 'Version');
    expect(Math.abs(geometry.dtBaseline - geometry.ddBaseline)).toBeLessThan(1.5);
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

    await app.sidebar.hoverOrchestratorRow(SEED_ORCHESTRATOR);
    const card = app.sidebar.orchestratorHoverCard(SEED_ORCHESTRATOR);
    await expect(card).toBeVisible();
    await expect(card).toContainText('Working, for');

    const geometry = await rowGeometry(card, 'Doing');
    expect(Math.abs(geometry.dtBaseline - geometry.ddBaseline)).toBeLessThan(1.5);
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

    await app.sidebar.hoverOrchestratorRow(SEED_ORCHESTRATOR);
    const card = app.sidebar.orchestratorHoverCard(SEED_ORCHESTRATOR);
    await expect(card).toBeVisible();
    await expect(card).toContainText('Working, for');
    await expect(card).toContainText('Shell running');

    // The "Doing" dd now stacks two lines (busy + shell). Baseline alignment
    // applies to the row's first line, not the value's own bottom edge.
    const geometry = await rowGeometry(card, 'Doing');
    expect(Math.abs(geometry.dtBaseline - geometry.ddBaseline)).toBeLessThan(1.5);
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

    await app.sidebar.hoverOrchestratorRow(SEED_ORCHESTRATOR);
    const card = app.sidebar.orchestratorHoverCard(SEED_ORCHESTRATOR);
    await expect(card).toBeVisible();
    await expect(card).toContainText(SEED_ENV_ALPHA);

    // The "wide" variant col-spans both dt and dd, so they land in separate
    // grid rows rather than sharing one -- items-baseline has no sibling to
    // align them against, and the label must still render fully above the
    // value it labels.
    const geometry = await rowGeometry(card, 'Environments');
    expect(geometry.dtBottom).toBeLessThanOrEqual(geometry.ddTop + 1);
  });
});
