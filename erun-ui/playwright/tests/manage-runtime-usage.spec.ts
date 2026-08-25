import type { Page } from '@playwright/test';

import { test, expect } from '../fixtures/erunApp.js';

// stubRuntimeUsage makes LoadRuntimeUsage return a fixed reading instead of
// falling through to the harness's stub kubectl (which has no cluster and so
// cannot exec into a pod). Mirrors env-init.spec.ts's stubDialogCluster
// pattern for LoadRuntimeResourceStatus.
async function stubRuntimeUsage(page: Page, body: unknown): Promise<void> {
  await page.route('**/__erun_invoke', async (route, request) => {
    const parsed = JSON.parse(request.postData() ?? '{}') as { method: string };
    if (parsed.method === 'LoadRuntimeUsage') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: body }),
      });
    }
    await route.continue();
  });
}

test.describe('manage dialog runtime usage panel', () => {
  test('the Runtime tab shows CPU, memory and disk usage for the environment itself', async ({
    app,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    await stubRuntimeUsage(app.page, {
      tenant,
      environment,
      available: true,
      message: 'This environment: CPU 45.0% of a 2.00-core quota, memory 1.5 GiB of 2.0 GiB (75%).',
      cpu: {
        available: true,
        quotaCores: 2,
        quota: '2.00 cores',
        utilizationPercent: 45,
        utilization: '45.0%',
      },
      memory: {
        available: true,
        currentBytes: 1610612736,
        current: '1.5 GiB',
        peakBytes: 1932735283,
        peak: '1.8 GiB',
        limitBytes: 2147483648,
        limit: '2.0 GiB',
        percentOfLimit: 75,
        oomKills: 0,
      },
      disk: [
        {
          mount: '/home/erun',
          available: true,
          totalBytes: 107374182400,
          total: '100.0 GiB',
          usedBytes: 96636764160,
          used: '90.0 GiB',
          percentUsed: 90,
          percent: '90.0%',
        },
      ],
      warnings: ['/home/erun is at 90% disk usage (warns at 90%)'],
    });

    await app.sidebar.openManageDialogViaKeyboard(tenant, environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    // The panel sits directly under the resource sliders: how close the
    // environment already is to its own limits is the evidence for moving
    // them.
    const panel = app.manageDialog.runtimeUsagePanel();
    await expect(panel).toBeVisible();
    await expect(app.manageDialog.runtimeUsageRefreshButton()).toBeVisible();

    await expect(panel).toContainText('45.0% of a 2.00 cores quota');
    await expect(panel).toContainText('1.5 GiB of 2.0 GiB (75%)');
    await expect(panel).toContainText('1.8 GiB');
    await expect(panel).toContainText('90.0 GiB of 100.0 GiB (90%)');

    // The disk-usage warning the reader already produces must surface, not
    // just the raw figures — it is what makes the reading actionable.
    await expect(panel).toContainText('/home/erun is at 90% disk usage (warns at 90%)');

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('a field the reader could not measure renders as unavailable, never as 0%', async ({
    app,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    await stubRuntimeUsage(app.page, {
      tenant,
      environment,
      available: true,
      message: 'This environment: memory 512 MiB used (no limit set).',
      // cgroup v1: the reader could not measure CPU at all.
      cpu: {
        available: false,
        unavailable:
          'cgroup v2 not detected under /sys/fs/cgroup; CPU usage needs cpu.max/cpu.stat',
      },
      // An unlimited container: a real reading with no ceiling to divide by.
      memory: {
        available: true,
        unlimited: true,
        currentBytes: 536870912,
        current: '512 MiB',
        peakBytes: 536870912,
        peak: '512 MiB',
        oomKills: 0,
      },
      disk: [
        {
          mount: '/home/erun',
          available: false,
          unavailable: 'df did not report usage for /home/erun',
        },
      ],
    });

    await app.sidebar.openManageDialogViaKeyboard(tenant, environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    const panel = app.manageDialog.runtimeUsagePanel();
    await expect(panel).toBeVisible();

    // The unmeasurable CPU reading must say so, not render "0.0%" or "0
    // cores" — a confident zero would read as "idle" rather than "unknown".
    await expect(panel).toContainText(
      'Unavailable — cgroup v2 not detected under /sys/fs/cgroup; CPU usage needs cpu.max/cpu.stat',
    );
    await expect(panel).not.toContainText('0.0%');

    // An unlimited container is a real, available reading and must render its
    // current/peak figures, but with no synthesized limit or percentage.
    await expect(panel).toContainText('512 MiB of no limit set');

    // The unreadable disk mount must say so, never "0%" used.
    await expect(panel).toContainText('Unavailable — df did not report usage for /home/erun');
    await expect(panel).not.toContainText('0% used');

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
