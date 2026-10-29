import type { Page } from '@playwright/test';

import { test, expect, withTestBudget } from '../../../fixtures/erunApp.js';

// The harness kubectl has no cluster to exec into.
async function stubRuntimeUsage(
  page: Page,
  body: unknown,
  options: { holdMs?: number } = {},
): Promise<void> {
  let holdUntil = 0;
  await page.route('**/__erun_invoke', async (route, request) => {
    const parsed = JSON.parse(request.postData() ?? '{}') as { method: string };
    if (parsed.method === 'LoadRuntimeUsage') {
      // Anchored to the request so the delay is independent of dialog open time; only the first read is held.
      if (options.holdMs && holdUntil === 0) {
        holdUntil = Date.now() + options.holdMs;
      }
      const remaining = holdUntil - Date.now();
      if (remaining > 0) {
        await new Promise((resolve) => setTimeout(resolve, remaining));
      }
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

    const panel = app.manageDialog.runtimeUsagePanel();
    await expect(panel).toBeVisible();
    await expect(app.manageDialog.runtimeUsageRefreshButton()).toBeVisible();

    // Every read is a stubbed round trip, so it waits on the test's budget, not expect's 10s.

    await expect(panel).toContainText('45.0%', withTestBudget());
    await expect(panel).toContainText('of a 2.00 cores quota', withTestBudget());
    await expect(panel).toContainText('1.5 GiB of 2.0 GiB', withTestBudget());
    await expect(panel).toContainText('75% of the limit', withTestBudget());
    await expect(panel).toContainText('1.8 GiB', withTestBudget());
    await expect(panel).toContainText('90.0 GiB of 100.0 GiB', withTestBudget());
    await expect(panel).toContainText('90% used', withTestBudget());

    // An unmeasured field must add no meter.
    await expect(panel.getByRole('meter')).toHaveCount(3, withTestBudget());
    await expect(panel.getByRole('meter', { name: 'Memory' })).toHaveAttribute(
      'aria-valuenow',
      '75',
      withTestBudget(),
    );

    // Severity is in the accessible name too; colour alone doesn't reach forced-colors.
    await expect(panel.getByRole('meter', { name: /Disk .* \(warning\)/ })).toHaveAttribute(
      'aria-valuenow',
      '90',
      withTestBudget(),
    );
    await expect(panel.getByRole('meter', { name: /Memory \(/ })).toHaveCount(0);

    await expect(panel).toContainText(
      '/home/erun is at 90% disk usage (warns at 90%)',
      withTestBudget(),
    );

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
      // cgroup v1: CPU unmeasurable.
      cpu: {
        available: false,
        unavailable:
          'cgroup v2 not detected under /sys/fs/cgroup; CPU usage needs cpu.max/cpu.stat',
      },
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

    // A confident zero would read as "idle" rather than "unknown".
    await expect(panel).toContainText(
      'Unavailable — cgroup v2 not detected under /sys/fs/cgroup; CPU usage needs cpu.max/cpu.stat',
      withTestBudget(),
    );
    await expect(panel).not.toContainText('0.0%');

    await expect(panel).toContainText('512 MiB', withTestBudget());
    await expect(panel).toContainText('no limit set', withTestBudget());

    await expect(panel).toContainText(
      'Unavailable — df did not report usage for /home/erun',
      withTestBudget(),
    );
    await expect(panel).not.toContainText('0% used');

    // A zero-width bar would read as "0%, idle" rather than "unknown".
    await expect(panel.getByRole('meter')).toHaveCount(0);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // Unstubbed: the real probe fails against the harness kubectl. Timeout vs.
  // signal-kill classification is covered in Go.
  test('an unreachable pod fails soft with a stated reason, never a blank panel', async ({
    app,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    await app.sidebar.openManageDialogViaKeyboard(tenant, environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    const panel = app.manageDialog.runtimeUsagePanel();
    await expect(panel).toBeVisible();
    await expect(panel).toContainText(
      "Cannot read this environment's resource usage",
      withTestBudget(),
    );

    await expect(panel).not.toContainText('signal:');

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // Builds run in the erun-dind sidecar's cgroup, so the near-idle runtime figure
  // alone can't tell a healthy build from a wedged one.
  test('a build-capable environment shows the erun-dind sidecar, not just the near-idle runtime', async ({
    app,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    await stubRuntimeUsage(app.page, {
      tenant,
      environment,
      available: true,
      message:
        'This environment: CPU 0.2% of a 12.00 cores quota, memory 1.1 GiB of 23.0 GiB (5%), ' +
        'excluding builds (they run in the erun-dind sidecar).',
      excludesBuilds: true,
      cpu: {
        available: true,
        quotaCores: 12,
        quota: '12.00 cores',
        utilizationPercent: 0.2,
        utilization: '0.2%',
      },
      memory: {
        available: true,
        currentBytes: 1181116006,
        current: '1.1 GiB',
        limitBytes: 24696061952,
        limit: '23.0 GiB',
        percentOfLimit: 5,
        oomKills: 0,
      },
      dind: {
        cpu: {
          available: true,
          quotaCores: 8,
          quota: '8.00 cores',
          utilizationPercent: 91.5,
          utilization: '91.5%',
        },
        memory: {
          available: true,
          currentBytes: 20830591385,
          current: '19.4 GiB',
          limitBytes: 21474836480,
          limit: '20.0 GiB',
          percentOfLimit: 97,
          oomKills: 0,
        },
      },
    });

    await app.sidebar.openManageDialogViaKeyboard(tenant, environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    const panel = app.manageDialog.runtimeUsagePanel();
    await expect(panel).toBeVisible();
    await expect(panel).toContainText('0.2%', withTestBudget());

    await expect(panel).toContainText(
      'Builds — the erun-dind sidecar every image build runs in',
      withTestBudget(),
    );
    await expect(panel).toContainText('91.5%', withTestBudget());
    await expect(panel).toContainText('19.4 GiB of 20.0 GiB', withTestBudget());

    await expect(panel.getByRole('meter', { name: 'Build CPU' })).toHaveAttribute(
      'aria-valuenow',
      '92',
      withTestBudget(),
    );
    await expect(panel.getByRole('meter', { name: 'Build memory' })).toHaveAttribute(
      'aria-valuenow',
      '97',
      withTestBudget(),
    );
    await expect(panel.getByRole('meter')).toHaveCount(4, withTestBudget());

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // With no cpu.max quota there is no ceiling, so cumulative CPU-seconds render without a bar.
  test('a sidecar with no CPU quota reports cumulative CPU-seconds, not an idle zero', async ({
    app,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    await stubRuntimeUsage(app.page, {
      tenant,
      environment,
      available: true,
      message:
        'This environment: CPU 0.6% of a 12.00 cores quota, memory 1.1 GiB of 23.0 GiB (5%), ' +
        'excluding builds (they run in the erun-dind sidecar).',
      excludesBuilds: true,
      cpu: {
        available: true,
        quotaCores: 12,
        quota: '12.00 cores',
        utilizationPercent: 0.6,
        utilization: '0.6%',
      },
      memory: {
        available: true,
        currentBytes: 1181116006,
        current: '1.1 GiB',
        limitBytes: 24696061952,
        limit: '23.0 GiB',
        percentOfLimit: 5,
        oomKills: 0,
      },
      dind: {
        cpu: {
          available: false,
          unavailable:
            'cpu.max reports no quota (unlimited or not readable); utilisation needs a quota to measure against',
          usageUsec: 385919164,
        },
        memory: {
          available: true,
          unlimited: true,
          currentBytes: 536870912,
          current: '512 MiB',
          oomKills: 0,
        },
      },
    });

    await app.sidebar.openManageDialogViaKeyboard(tenant, environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    const panel = app.manageDialog.runtimeUsagePanel();
    await expect(panel).toBeVisible();
    await expect(panel).toContainText('386 CPU-s', withTestBudget());
    await expect(panel).toContainText(
      'cumulative, no CPU quota to measure a rate against',
      withTestBudget(),
    );

    await expect(panel).toContainText('Build memory', withTestBudget());
    await expect(panel).toContainText('512 MiB', withTestBudget());
    await expect(panel).toContainText('no limit set', withTestBudget());

    await expect(panel.getByRole('meter', { name: 'Build CPU' })).toHaveCount(0);
    await expect(panel.getByRole('meter', { name: 'Build memory' })).toHaveCount(0);
    await expect(panel.getByRole('meter')).toHaveCount(2, withTestBudget());

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // A failed sidecar read must not render as an environment without a sidecar.
  test('a build-capable environment whose sidecar could not be read says so in the Builds zone', async ({
    app,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    await stubRuntimeUsage(app.page, {
      tenant,
      environment,
      available: true,
      message:
        'This environment: CPU 1.3% of a 12.00 cores quota, memory 1% of 23.0 GiB, ' +
        'excluding builds (they run in the erun-dind sidecar).',
      excludesBuilds: true,
      cpu: {
        available: true,
        quotaCores: 12,
        quota: '12.00 cores',
        utilizationPercent: 1.3,
        utilization: '1.3%',
      },
      memory: {
        available: true,
        currentBytes: 246960619,
        current: '230 MiB',
        limitBytes: 24696061952,
        limit: '23.0 GiB',
        percentOfLimit: 1,
        oomKills: 0,
      },
    });

    await app.sidebar.openManageDialogViaKeyboard(tenant, environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    const panel = app.manageDialog.runtimeUsagePanel();
    await expect(panel).toBeVisible();
    await expect(panel).toContainText('1.3%', withTestBudget());

    await expect(panel).toContainText(
      'Builds — the erun-dind sidecar every image build runs in',
      withTestBudget(),
    );
    await expect(panel).toContainText('the sidecar did not answer', withTestBudget());
    // A not-read sidecar must never borrow a bar meaning "measured".
    await expect(panel.getByRole('meter', { name: 'Build CPU' })).toHaveCount(0);
    await expect(panel.getByRole('meter', { name: 'Build memory' })).toHaveCount(0);
    await expect(panel.getByRole('meter')).toHaveCount(2, withTestBudget());

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // The hold is just past expect's 10s default, so the read must be waited out on
  // the test's own budget.
  test('a reading that lands past the step cap is waited out, not cut off', async ({
    app,
    seededEnv,
  }) => {
    test.setTimeout(60_000);
    const { tenant, environment } = seededEnv;
    await stubRuntimeUsage(
      app.page,
      {
        tenant,
        environment,
        available: true,
        message:
          'This environment: CPU 45.0% of a 2.00-core quota, memory 1.5 GiB of 2.0 GiB (75%).',
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
      },
      { holdMs: 12_000 },
    );

    await app.sidebar.openManageDialogViaKeyboard(tenant, environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    const panel = app.manageDialog.runtimeUsagePanel();
    await expect(panel).toContainText('45.0%', withTestBudget());

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
