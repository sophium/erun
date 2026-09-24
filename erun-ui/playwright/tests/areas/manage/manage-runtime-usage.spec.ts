import type { Page } from '@playwright/test';

import { test, expect } from '../../../fixtures/erunApp.js';

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

    // The exact figures a slider decision needs, each beside its own meter.
    await expect(panel).toContainText('45.0%');
    await expect(panel).toContainText('of a 2.00 cores quota');
    await expect(panel).toContainText('1.5 GiB of 2.0 GiB');
    await expect(panel).toContainText('75% of the limit');
    await expect(panel).toContainText('1.8 GiB');
    await expect(panel).toContainText('90.0 GiB of 100.0 GiB');
    await expect(panel).toContainText('90% used');

    // A percentage against a limit is a magnitude, so each measured field
    // renders a meter carrying its own value -- CPU, memory and the one disk
    // mount. Asserting the count pins that an unmeasured field adds none.
    await expect(panel.getByRole('meter')).toHaveCount(3);
    await expect(panel.getByRole('meter', { name: 'Memory' })).toHaveAttribute(
      'aria-valuenow',
      '75',
    );

    // Severity lives in the meter itself, not only in the warning line below,
    // and it is carried in the accessible name too -- colour alone would not
    // reach a colourblind reader or forced-colors mode. Disk is at its 90%
    // threshold; memory at 75% is below its 85% one and stays unmarked.
    await expect(panel.getByRole('meter', { name: /Disk .* \(warning\)/ })).toHaveAttribute(
      'aria-valuenow',
      '90',
    );
    await expect(panel.getByRole('meter', { name: /Memory \(/ })).toHaveCount(0);

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
    await expect(panel).toContainText('512 MiB');
    await expect(panel).toContainText('no limit set');

    // The unreadable disk mount must say so, never "0%" used.
    await expect(panel).toContainText('Unavailable — df did not report usage for /home/erun');
    await expect(panel).not.toContainText('0% used');

    // The sharpest form of the fail-soft contract: not one of these three
    // fields was measurable against a limit, so the panel renders NO meter at
    // all. A zero-width bar would read as "0%, idle" rather than "unknown" --
    // which is the confident-wrong-number failure #1336 exists to prevent.
    await expect(panel.getByRole('meter')).toHaveCount(0);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // The seeded env is inert and never deployed, so the harness's stub kubectl
  // (unmocked here, unlike the two tests above) fails the probe for real
  // rather than through a stubbed response. Visibility of system status
  // (Nielsen #1) requires the panel to say so rather than render blank.
  //
  // The stub kubectl fails fast, so this exercises the classifier's "keep the
  // raw cause" branch, not the deadline-vs-external-kill classification --
  // this offline harness cannot make a probe actually time out or get
  // signal-killed. Those branches are covered by the Go suite instead
  // (erun-ui/runtime_probe_error_test.go and
  // TestLoadRuntimeUsageReportsOwnTimeoutNotSignalKilled in
  // erun-ui/runtime_usage_test.go).
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
    await expect(panel).toContainText("Cannot read this environment's resource usage");

    await expect(panel).not.toContainText('signal:');

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // The reported defect in its live form: a build environment read "Busy —
  // holding: release 1.0.302" beside a CPU of 0.2%. The figure was the runtime
  // container's, and every image build runs in the erun-dind sidecar's own
  // cgroup — a release lane spends its time waiting on bounded `erun exec job
  // await` calls, so that container is near-idle by construction and the
  // headline number cannot tell a healthy build from a wedged one. The reader
  // already acquires the sidecar's reading; this pins that the panel shows it,
  // and labels which domain each figure belongs to.
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
    // The runtime container's own figure is still shown, and still small.
    await expect(panel).toContainText('0.2%');

    // The sidecar is the container the work is actually in, named so the two
    // CPU figures cannot be confused for one another.
    await expect(panel).toContainText('Builds — the erun-dind sidecar every image build runs in');
    await expect(panel).toContainText('91.5%');
    await expect(panel).toContainText('19.4 GiB of 20.0 GiB');

    // Each figure keeps its own meter, under its own label.
    await expect(panel.getByRole('meter', { name: 'Build CPU' })).toHaveAttribute(
      'aria-valuenow',
      '92',
    );
    await expect(panel.getByRole('meter', { name: 'Build memory' })).toHaveAttribute(
      'aria-valuenow',
      '97',
    );
    await expect(panel.getByRole('meter')).toHaveCount(4);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // cpu.max declares no quota on many sidecars (a build is meant to be able to
  // use the node), so no percentage can exist there — and "Unavailable" alone
  // would leave a build environment's only visible CPU figure the runtime
  // container's near-zero, which is where the operator started. The cumulative
  // counter is a real measurement, stated as CPU-seconds because it is not a
  // rate: it gets no bar, since there is no ceiling to be a fraction of.
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
    await expect(panel).toContainText('386 CPU-s');
    await expect(panel).toContainText('cumulative, no CPU quota to measure a rate against');

    // A sidecar memory reading with no ceiling is a real reading, stated
    // without a limit rather than rendered as unknown.
    await expect(panel).toContainText('Build memory');
    await expect(panel).toContainText('512 MiB');
    await expect(panel).toContainText('no limit set');

    // Neither of the sidecar's figures had a ceiling, so neither draws a bar:
    // the two meters are the runtime container's own. A zero-width bar here
    // would read as "0%, idle" rather than "no ceiling to measure against".
    await expect(panel.getByRole('meter', { name: 'Build CPU' })).toHaveCount(0);
    await expect(panel.getByRole('meter', { name: 'Build memory' })).toHaveCount(0);
    await expect(panel.getByRole('meter')).toHaveCount(2);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
