import type { Page, Route } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_ORCHESTRATOR, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// Pins every field both hover cards render, with real values, so a restyle can't silently drop one.

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
          cpu: {
            available: true,
            utilization: '12.0%',
            utilizationPercent: 12,
            quota: '2.00 cores',
          },
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
  // The usage sweep can re-render the row and drop the card, so every check re-hovers and re-emits
  // within one retry.
  await expect(async () => {
    await emitStaleEnvUsage(page);
    await page.mouse.move(0, 0);
    await app.sidebar.hoverEnvironmentRow(SEED_TENANT, SEED_ENV_ALPHA);
    await expect(card).toBeVisible({ timeout: 1_000 });

    await expect(card).toContainText(`${SEED_TENANT} / ${SEED_ENV_ALPHA}`, { timeout: 1_000 });
    await expect(card.getByText('Local', { exact: true })).toBeVisible({ timeout: 1_000 });

    await expect(card.getByText('Version', { exact: true })).toBeVisible({ timeout: 1_000 });
    await expect(card).toContainText('1.0.0', { timeout: 1_000 });

    await expect(card.getByText('Working on', { exact: true })).toBeVisible({ timeout: 1_000 });
    await expect(card).toContainText('feature/1694-hover-card-type-scale', { timeout: 1_000 });
    await expect(card).toContainText('#1694', { timeout: 1_000 });
    await expect(card).toContainText('Neither sidebar hover card has a type scale', {
      timeout: 1_000,
    });

    await expect(card.getByText('Activity', { exact: true })).toBeVisible({ timeout: 1_000 });
    await expect(card).toContainText('Idle', { timeout: 1_000 });

    await expect(card.getByText('CPU', { exact: true })).toBeVisible({ timeout: 1_000 });
    await expect(card.getByText('Memory', { exact: true })).toBeVisible({ timeout: 1_000 });
    await expect(card).toContainText('12.0%', { timeout: 1_000 });
    await expect(card).toContainText('of 2048Mi', { timeout: 1_000 });
    await expect(card.locator('[data-decile-fill]')).toHaveCount(2, { timeout: 1_000 });
    await expect(card).toContainText('Stale', { timeout: 1_000 });
    await expect(card).toContainText('ago', { timeout: 1_000 });
  }).toPass();
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

  // Every check retries with re-hover; see the env card test above.
  await app.sidebar.readOrchestratorHoverCard(SEED_ORCHESTRATOR, async (card) => {
    await expect(card).toBeVisible({ timeout: 1_000 });

    await expect(card).toContainText(SEED_ORCHESTRATOR, { timeout: 1_000 });
    await expect(card.getByText('Transient', { exact: true })).toBeVisible({ timeout: 1_000 });

    await expect(card.getByText('Status', { exact: true })).toBeVisible({ timeout: 1_000 });
    await expect(card).toContainText('Running', { timeout: 1_000 });

    await expect(card.getByText('Doing', { exact: true })).toBeVisible({ timeout: 1_000 });
    await expect(card).toContainText('Working, for', { timeout: 1_000 });
    await expect(card).toContainText('Shell running for', { timeout: 1_000 });
    await expect(card).toContainText('yarn build', { timeout: 1_000 });

    await expect(card.getByText('Environments', { exact: true })).toBeVisible({ timeout: 1_000 });
    await expect(card).toContainText(`${SEED_TENANT} / ${SEED_ENV_ALPHA}`, { timeout: 1_000 });

    // Nudges render only while running.
    await expect(card.getByText('Nudges', { exact: true })).toBeVisible({ timeout: 1_000 });
  });
});
