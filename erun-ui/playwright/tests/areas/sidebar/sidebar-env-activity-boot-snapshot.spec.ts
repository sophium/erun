import type { Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';

// env-activity only fires on transitions and the Go poller outlives a reload, so a reload must seed
// a busy env from LoadState.
// Go-side seeding is covered by erun-ui/environment_activity_test.go.

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
