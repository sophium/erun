import type { Locator, Page, Route } from '@playwright/test';

import { expect, test, withTestBudget } from '../../../fixtures/erunApp.js';

// 'activity:state' events and ListDeploys snapshots both write the queue; a snapshot
// taken while running and applied after the finish must not revert the entry.
// The route forces that overlap; both writers run their real paths.

const SPEC_TENANT = 'snapshot-order';
const SPEC_ENV = 'spec-env';
const SPEC_TARGET = `${SPEC_TENANT}/${SPEC_ENV}`;

// startedAt identifies the run; lastUpdated is what the ordering reads.
const STARTED_AT = '2026-08-24T00:00:00.000Z';

// The second pair is 800µs apart: both floor to the same millisecond under Date.parse,
// so the reducer must compare the stamps' own digits. Titles stay literal so they are greppable.
const CASES = [
  {
    title:
      'a ListDeploys snapshot taken before an entry finished cannot revert it to running -- orders a snapshot seconds behind the finish',
    entryId: 'snapshot-order-apart',
    runningAt: '2026-08-24T00:00:00.000Z',
    finishedAt: '2026-08-24T00:00:05.000Z',
  },
  {
    title:
      'a ListDeploys snapshot taken before an entry finished cannot revert it to running -- orders a snapshot inside the same millisecond as the finish',
    entryId: 'snapshot-order-tied',
    runningAt: '2026-08-24T00:00:00.000100000Z',
    finishedAt: '2026-08-24T00:00:00.000900000Z',
  },
];

interface StagedEntry {
  id: string;
  command: string;
  tenant: string;
  environment: string;
  status: string;
  startedAt: string;
  lastUpdated: string;
  endedAt?: string;
  source: string;
  actionKind: string;
  summary: string;
}

function stagedEntry(id: string, status: 'running' | 'succeeded', at: string): StagedEntry {
  return {
    id,
    command: 'deploy',
    tenant: SPEC_TENANT,
    environment: SPEC_ENV,
    status,
    startedAt: STARTED_AT,
    lastUpdated: at,
    endedAt: status === 'succeeded' ? at : undefined,
    source: 'action',
    actionKind: 'deploy',
    summary: `deploy ${SPEC_TARGET}`,
  };
}

function emitActivityState(page: Page, entry: StagedEntry): Promise<void> {
  return page.evaluate((payload) => {
    (
      window as unknown as { runtime: { EventsEmit: (n: string, ...a: unknown[]) => void } }
    ).runtime.EventsEmit('activity:state', payload);
  }, entry);
}

// The shim POSTs { method, args } to /__erun_invoke and unwraps { data }.
function isListDeploys(route: Route): boolean {
  try {
    const body = JSON.parse(route.request().postData() ?? '') as { method?: string };
    return body.method === 'ListDeploys';
  } catch {
    return false;
  }
}

for (const { title, entryId, runningAt, finishedAt } of CASES) {
  test(title, async ({ app, page }) => {
    const now = page.getByRole('region', { name: 'Now' });
    const recent = page.getByRole('region', { name: 'Recent' });
    // Scoped to this entry's card; each case uses its own id on the shared backend.
    const cardIn = (section: Locator) =>
      section.locator('article').filter({ hasText: SPEC_TARGET });
    let staleSnapshotServed = false;

    await app.activityDrawer.open();

    await emitActivityState(page, stagedEntry(entryId, 'running', runningAt));
    await cardIn(now).waitFor({ state: 'visible' });

    await emitActivityState(page, stagedEntry(entryId, 'succeeded', finishedAt));
    await cardIn(recent).waitFor({ state: 'visible' });
    await expect(cardIn(now)).toBeHidden();

    // Dismissing invalidates the Deploys tag; the route answers with the older copy.
    await page.route('**/__erun_invoke', async (route) => {
      if (!isListDeploys(route)) {
        await route.continue();
        return;
      }
      staleSnapshotServed = true;
      await route.fulfill({ json: { data: [stagedEntry(entryId, 'running', runningAt)] } });
    });
    await cardIn(recent).getByRole('button', { name: 'Dismiss', exact: true }).click();

    // Prove the stale snapshot was actually served, or the case passes vacuously.
    await expect.poll(() => staleSnapshotServed, withTestBudget()).toBe(true);

    // A revert would move the card back to "Now".
    await expect(cardIn(recent)).toBeVisible(withTestBudget());
    await expect(cardIn(now)).toBeHidden();
  });
}
