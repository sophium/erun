import type { Page, Route } from '@playwright/test';

import { expect, test } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_ORCHESTRATOR, SEED_TENANT } from '../fixtures/seedRoot.js';

// #1694 restyled both hover cards onto a shared type scale (HoverCardRow).
// A restyle that drops or truncates a field is a worse regression than the
// inconsistent type treatments it replaced, so this spec pins every field
// both cards rendered before the change, populated with real (not
// placeholder) values so a dropped field can't hide behind an empty string.

async function stubWorkingIssue(page: Page): Promise<void> {
  await page.route('**/__erun_invoke', async (route: Route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method?: string };
    if (body.method === 'EnvironmentWorkingIssue') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({
          data: {
            available: true,
            branch: 'feature/1694-hover-card-type-scale',
            issueNumber: 1694,
            issueTitle: 'Neither sidebar hover card has a type scale',
          },
        }),
      });
    }
    await route.continue();
  });
}

async function emitStaleEnvUsage(page: Page): Promise<void> {
  await page.evaluate(
    ({ tenant, environment }) => {
      const runtime = (
        window as unknown as {
          runtime: { EventsEmit: (name: string, ...args: unknown[]) => void };
        }
      ).runtime;
      runtime.EventsEmit('env-usage', {
        tenant,
        environment,
        usage: {
          tenant,
          environment,
          available: true,
          cpu: { available: true, utilization: '12.0%', quota: '2.00 cores' },
          memory: {
            available: true,
            current: '512Mi',
            limit: '2048Mi',
            percentOfLimit: 25,
            oomKills: 0,
          },
        },
        observedAtUnix: Math.floor(Date.now() / 1000) - 600,
        staleAfterSeconds: 90,
      });
    },
    { tenant: SEED_TENANT, environment: SEED_ENV_ALPHA },
  );
}

test('the env hover card still renders every field: version, branch+issue, activity, and a stale usage reading', async ({
  app,
  page,
}) => {
  await stubWorkingIssue(page);
  await app.reboot();

  const card = app.sidebar.envHoverCard(SEED_TENANT, SEED_ENV_ALPHA);
  await expect(async () => {
    await emitStaleEnvUsage(page);
    await page.mouse.move(0, 0);
    await app.sidebar.hoverEnvironmentRow(SEED_TENANT, SEED_ENV_ALPHA);
    await expect(card).toBeVisible({ timeout: 1_000 });
    await expect(card).toContainText('Stale', { timeout: 1_000 });
  }).toPass({ timeout: 20_000 });

  // Header: tenant/env title plus the Local badge.
  await expect(card).toContainText(`${SEED_TENANT} / ${SEED_ENV_ALPHA}`);
  await expect(card.getByText('Local', { exact: true })).toBeVisible();

  // Version row.
  await expect(card.getByText('Version', { exact: true })).toBeVisible();
  await expect(card).toContainText('1.0.0');

  // Working-on row: branch, issue number, and issue title all present.
  await expect(card.getByText('Working on', { exact: true })).toBeVisible();
  await expect(card).toContainText('feature/1694-hover-card-type-scale');
  await expect(card).toContainText('#1694');
  await expect(card).toContainText('Neither sidebar hover card has a type scale');

  // Activity row.
  await expect(card.getByText('Activity', { exact: true })).toBeVisible();
  await expect(card).toContainText('Idle');

  // Usage row: headline figures, staleness flag, and the reading's age.
  await expect(card.getByText('Usage', { exact: true })).toBeVisible();
  await expect(card).toContainText('CPU 12.0%');
  await expect(card).toContainText('Mem 25% of 2048Mi');
  await expect(card).toContainText('Stale');
  await expect(card).toContainText('ago');
});

test('the orchestrator hover card still renders every field: status, doing, linked environments, and nudges', async ({
  app,
  page,
}) => {
  const busyAtUnix = Math.floor(Date.now() / 1000) - 252;
  await page.route('**/__erun_invoke', async (route: Route, request) => {
    const parsed = JSON.parse(request.postData() ?? '{}') as { method?: string };
    if (parsed.method === 'ListOrchestrators') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({
          data: [
            {
              id: SEED_ORCHESTRATOR,
              name: SEED_ORCHESTRATOR,
              environments: [
                { tenant: SEED_TENANT, environment: SEED_ENV_ALPHA, directory: '/tmp/a' },
              ],
              tenants: [],
              directories: [],
              sessionId: 4242,
              status: 'running',
              busy: true,
              busyAtUnix,
              transient: true,
              shellRunning: true,
              shellCommand: 'yarn build',
              shellStartedAtUnix: busyAtUnix,
            },
          ],
        }),
      });
    }
    await route.continue();
  });
  await app.reboot();

  await app.sidebar.orchestratorRowButton(SEED_ORCHESTRATOR).hover();
  const card = app.sidebar.orchestratorHoverCard(SEED_ORCHESTRATOR);
  await expect(card).toBeVisible();

  // Header: orchestrator name plus the Transient badge.
  await expect(card).toContainText(SEED_ORCHESTRATOR);
  await expect(card.getByText('Transient', { exact: true })).toBeVisible();

  // Status row.
  await expect(card.getByText('Status', { exact: true })).toBeVisible();
  await expect(card).toContainText('Running');

  // Doing row: both the working turn and the background shell are named.
  await expect(card.getByText('Doing', { exact: true })).toBeVisible();
  await expect(card).toContainText('Working, for');
  await expect(card).toContainText('Shell running for');
  await expect(card).toContainText('yarn build');

  // Environments row: linked env's name and status line.
  await expect(card.getByText('Environments', { exact: true })).toBeVisible();
  await expect(card).toContainText(`${SEED_TENANT} / ${SEED_ENV_ALPHA}`);

  // Nudges row (rendered only while running).
  await expect(card.getByText('Nudges', { exact: true })).toBeVisible();
});
