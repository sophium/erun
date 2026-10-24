import type { Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';

// The env-activity Wails event only fires on a transition, and the Go
// poller's memory (app.envActivity) survives a page reload the Redux store
// does not, so a reload must seed a still-busy environment from LoadState
// rather than render it idle until the next transition.
//
// This spec drives the frontend half directly, mirroring
// sidebar-orchestrator-busy-snapshot.spec.ts: it stubs LoadState over
// /__erun_invoke to answer with a busy env snapshot, reboots the app (a real
// fresh mount via page.goto, not a simulated event), and asserts the row
// renders busy immediately, with no env-activity event ever emitted. The event path itself (env-activity flipping the dot
// while the app is already running) is covered by sidebar-env-activity.spec.ts;
// the Go-side seeding this snapshot depends on is covered by
// erun-ui/environment_activity_test.go
// (TestSeedEnvironmentActivitySnapshotsCarriesTheLastObservation,
// TestLoadStateSeedsEnvironmentActivityFromThePoller).

const TENANT = 'boot-snap';
const ENVIRONMENT = 'dev';

function loadStateWithActivity(busy: boolean): unknown {
  return {
    tenants: [
      {
        name: TENANT,
        defaultEnvironment: ENVIRONMENT,
        environments: [
          {
            name: ENVIRONMENT,
            type: 'local-agent',
            activity: {
              reachable: true,
              observed: true,
              outage: false,
              busy,
              detail: busy ? 'an agent is driving it over MCP' : '',
            },
          },
        ],
      },
    ],
    build: { version: '0.0.0-test' },
  };
}

async function stubLoadState(page: Page, busy: boolean): Promise<void> {
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method?: string };
    if (body.method === 'LoadState') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: loadStateWithActivity(busy) }),
      });
    }
    await route.continue();
  });
}

test.describe('sidebar env activity renders from the boot snapshot (#1216)', () => {
  test('a busy snapshot renders the row busy on a fresh mount, with no env-activity event ever emitted', async ({
    app,
    page,
  }) => {
    await stubLoadState(page, true);
    await app.reboot();

    const dot = app.sidebar.envOpenDot(TENANT, ENVIRONMENT);
    await expect(dot).toHaveAttribute('data-env-state', 'busy');
    await expect(dot).toHaveAccessibleName(
      `${TENANT} / ${ENVIRONMENT} is busy — an agent is driving it over MCP`,
    );
  });

  test('a non-busy snapshot renders the reachable dot, not busy', async ({ app, page }) => {
    await stubLoadState(page, false);
    await app.reboot();

    const dot = app.sidebar.envOpenDot(TENANT, ENVIRONMENT);
    await expect(dot).toHaveAttribute('data-env-state', 'running');
  });
});
