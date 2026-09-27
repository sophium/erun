import type { Locator, Page, Route } from '@playwright/test';

import { expect, test, withTestBudget } from '../../../fixtures/erunApp.js';

// The activity queue has two writers and nothing ordered them against each
// other. 'activity:state' (upsertActivityEntry) is applied the moment the Go
// store mutates, while ListDeploys is a snapshot taken at request time and
// applied later, when the query resolves and its effect runs. A snapshot taken
// while an entry was still running can therefore be applied after the store
// already finished it -- and a finished entry has no later emit to correct it,
// because finishing removes it from the store's active set and stops its
// container poller. The card then reads 'running' for good, and 'running' is
// what puts the entry in the drawer's "Now" section and holds the launcher's
// "(n active)" count up.
//
// The overlap this spec drives is forced, not coincidental: ListDeploys is
// stood in for by a route that answers with the copy taken before the finish,
// and the finish is delivered first. What is NOT staged is either writer --
// the finish arrives over the real EventsEmit -> /__erun_emit -> SSE -> the
// app's 'activity:state' listener, and the snapshot is applied by the real
// useListDeploysQuery resolution and its setActivityEntries effect. Only the
// timing that a loaded machine otherwise has to supply by luck is ours.
// The refetch trigger is a real one: dismissing any entry in the drawer
// invalidates the Deploys tag, which is the app's own path to a fresh
// snapshot (see the setActivityEntries comment in activitySlice.ts).

const SPEC_TENANT = 'snapshot-order';
const SPEC_ENV = 'spec-env';
const SPEC_TARGET = `${SPEC_TENANT}/${SPEC_ENV}`;

// Every copy of the staged entry carries the same startedAt: it identifies the
// run, not the write, and lastUpdated is what the ordering under test reads.
const STARTED_AT = '2026-08-24T00:00:00.000Z';

// Fixed stamps, not `new Date()`: the ordering has to come from the stamps
// alone, so both writers are pinned rather than taken from the clock.
//
// Two cases, because the resolution the reducer reads the stamps at is what
// decides the outcome. `lastUpdated` is the Go store's own write time in
// RFC3339Nano, so it can carry sub-millisecond digits; the second pair below
// is 800 microseconds apart, and Date.parse floors both of them onto the same
// millisecond. A reducer that compares parsed milliseconds ties there and
// hands the win to whichever copy it falls back to -- the stale snapshot --
// so the two writers have to be ordered by the stamp's own digits.
const CASES = [
  {
    label: 'orders a snapshot seconds behind the finish',
    entryId: 'snapshot-order-apart',
    runningAt: '2026-08-24T00:00:00.000Z',
    finishedAt: '2026-08-24T00:00:05.000Z',
  },
  {
    label: 'orders a snapshot inside the same millisecond as the finish',
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

for (const { label, entryId, runningAt, finishedAt } of CASES) {
  test(`a ListDeploys snapshot taken before an entry finished cannot revert it to running -- ${label}`, async ({
    app,
    page,
  }) => {
    const now = page.getByRole('region', { name: 'Now' });
    const recent = page.getByRole('region', { name: 'Recent' });
    // Scoped to this entry's own card: the seeded baseline already populates
    // "Recent", so a section-wide text or button locator would match its rows.
    // Each case stages its own id because the worker's backend is shared
    // across the specs in a file.
    const cardIn = (section: Locator) =>
      section.locator('article').filter({ hasText: SPEC_TARGET });
    let staleSnapshotServed = false;

    await app.activityDrawer.open();

    // The store reports the entry running.
    await emitActivityState(page, stagedEntry(entryId, 'running', runningAt));
    await cardIn(now).waitFor({ state: 'visible' });

    // The store finishes it. This is the newer of the two writes.
    await emitActivityState(page, stagedEntry(entryId, 'succeeded', finishedAt));
    await cardIn(recent).waitFor({ state: 'visible' });
    await expect(cardIn(now)).toBeHidden();

    // Now the snapshot taken while it was still running resolves. Dismissing the
    // entry is the real trigger -- it invalidates the Deploys tag -- and the
    // route stands in for the response with the older copy. DismissDeploy finds
    // nothing to remove in the backend, so the entry stays on screen and the only
    // thing that can change its status is the snapshot being applied.
    await page.route('**/__erun_invoke', async (route) => {
      if (!isListDeploys(route)) {
        await route.continue();
        return;
      }
      staleSnapshotServed = true;
      await route.fulfill({ json: { data: [stagedEntry(entryId, 'running', runningAt)] } });
    });
    await cardIn(recent).getByRole('button', { name: 'Dismiss', exact: true }).click();

    // The overlap itself is an observed condition, not assumed: the refetch has
    // to have been answered with the older copy before anything below can say
    // the snapshot was harmless. Without this the case would pass by never
    // applying a snapshot at all.
    await expect.poll(() => staleSnapshotServed, withTestBudget()).toBe(true);

    // The card must still read finished: same section, and no phantom entry left
    // in "Now". A revert shows up here as the card moving back to "Now".
    await expect(cardIn(recent)).toBeVisible(withTestBudget());
    await expect(cardIn(now)).toBeHidden();
  });
}
