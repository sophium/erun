import type { Page } from '@playwright/test';

import { test, expect, withTestBudget } from '../../../fixtures/erunApp.js';

// stubRuntimeUsage makes LoadRuntimeUsage return a fixed reading instead of
// falling through to the harness's stub kubectl (which has no cluster and so
// cannot exec into a pod). Mirrors env-init.spec.ts's stubDialogCluster
// pattern for LoadRuntimeResourceStatus.
async function stubRuntimeUsage(
  page: Page,
  body: unknown,
  options: { holdMs?: number } = {},
): Promise<void> {
  let holdUntil = 0;
  await page.route('**/__erun_invoke', async (route, request) => {
    const parsed = JSON.parse(request.postData() ?? '{}') as { method: string };
    if (parsed.method === 'LoadRuntimeUsage') {
      // holdMs makes this read land a fixed window after the request that
      // asked for it, so a step that is merely slow can be told apart from a
      // step that never converges. Anchored to the request (not to the stub's
      // own registration) so the delay is the same however long the dialog
      // took to open, and only the first read is held -- a refetch is a
      // separate step with its own convergence.
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

    // The panel sits directly under the resource sliders: how close the
    // environment already is to its own limits is the evidence for moving
    // them.
    const panel = app.manageDialog.runtimeUsagePanel();
    await expect(panel).toBeVisible();
    await expect(app.manageDialog.runtimeUsageRefreshButton()).toBeVisible();

    // Every read below is the answer to a LoadRuntimeUsage round trip, which
    // is a value this spec's own stub owns rather than a render that has
    // already happened -- so each carries withTestBudget(), the budget its
    // test declared, instead of expect's own 10s default. See the held-read
    // case at the end of this file for the reproduction.

    // The exact figures a slider decision needs, each beside its own meter.
    await expect(panel).toContainText('45.0%', withTestBudget());
    await expect(panel).toContainText('of a 2.00 cores quota', withTestBudget());
    await expect(panel).toContainText('1.5 GiB of 2.0 GiB', withTestBudget());
    await expect(panel).toContainText('75% of the limit', withTestBudget());
    await expect(panel).toContainText('1.8 GiB', withTestBudget());
    await expect(panel).toContainText('90.0 GiB of 100.0 GiB', withTestBudget());
    await expect(panel).toContainText('90% used', withTestBudget());

    // A percentage against a limit is a magnitude, so each measured field
    // renders a meter carrying its own value -- CPU, memory and the one disk
    // mount. Asserting the count pins that an unmeasured field adds none.
    await expect(panel.getByRole('meter')).toHaveCount(3, withTestBudget());
    await expect(panel.getByRole('meter', { name: 'Memory' })).toHaveAttribute(
      'aria-valuenow',
      '75',
      withTestBudget(),
    );

    // Severity lives in the meter itself, not only in the warning line below,
    // and it is carried in the accessible name too -- colour alone would not
    // reach a colourblind reader or forced-colors mode. Disk is at its 90%
    // threshold; memory at 75% is below its 85% one and stays unmarked.
    await expect(panel.getByRole('meter', { name: /Disk .* \(warning\)/ })).toHaveAttribute(
      'aria-valuenow',
      '90',
      withTestBudget(),
    );
    await expect(panel.getByRole('meter', { name: /Memory \(/ })).toHaveCount(0);

    // The disk-usage warning the reader already produces must surface, not
    // just the raw figures — it is what makes the reading actionable.
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
      withTestBudget(),
    );
    await expect(panel).not.toContainText('0.0%');

    // An unlimited container is a real, available reading and must render its
    // current/peak figures, but with no synthesized limit or percentage.
    await expect(panel).toContainText('512 MiB', withTestBudget());
    await expect(panel).toContainText('no limit set', withTestBudget());

    // The unreadable disk mount must say so, never "0%" used.
    await expect(panel).toContainText(
      'Unavailable — df did not report usage for /home/erun',
      withTestBudget(),
    );
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
    // The failure copy is this read's own outcome, so it waits on the read --
    // and this one is the unmocked read, the only panel here whose answer is a
    // real subprocess rather than a fulfilled stub.
    await expect(panel).toContainText(
      "Cannot read this environment's resource usage",
      withTestBudget(),
    );

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
    await expect(panel).toContainText('0.2%', withTestBudget());

    // The sidecar is the container the work is actually in, named so the two
    // CPU figures cannot be confused for one another.
    await expect(panel).toContainText(
      'Builds — the erun-dind sidecar every image build runs in',
      withTestBudget(),
    );
    await expect(panel).toContainText('91.5%', withTestBudget());
    await expect(panel).toContainText('19.4 GiB of 20.0 GiB', withTestBudget());

    // Each figure keeps its own meter, under its own label.
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
    await expect(panel).toContainText('386 CPU-s', withTestBudget());
    await expect(panel).toContainText(
      'cumulative, no CPU quota to measure a rate against',
      withTestBudget(),
    );

    // A sidecar memory reading with no ceiling is a real reading, stated
    // without a limit rather than rendered as unknown.
    await expect(panel).toContainText('Build memory', withTestBudget());
    await expect(panel).toContainText('512 MiB', withTestBudget());
    await expect(panel).toContainText('no limit set', withTestBudget());

    // Neither of the sidecar's figures had a ceiling, so neither draws a bar:
    // the two meters are the runtime container's own. A zero-width bar here
    // would read as "0%, idle" rather than "no ceiling to measure against".
    await expect(panel.getByRole('meter', { name: 'Build CPU' })).toHaveCount(0);
    await expect(panel.getByRole('meter', { name: 'Build memory' })).toHaveCount(0);
    await expect(panel.getByRole('meter')).toHaveCount(2, withTestBudget());

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // The sidecar reading failing is a state the panel has to state, not one it
  // can render as an environment without a sidecar. `dind` is absent either
  // way, and the figures above are the runtime container's alone: without this
  // the operator reads near-idle CPU and memory beside the reader's own
  // "excluding builds" prose with nothing saying the sidecar was never read.
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
      // No `dind` at all: the exec into the sidecar failed, and the reader
      // returns the runtime container's reading without it.
    });

    await app.sidebar.openManageDialogViaKeyboard(tenant, environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    const panel = app.manageDialog.runtimeUsagePanel();
    await expect(panel).toBeVisible();
    await expect(panel).toContainText('1.3%', withTestBudget());

    // The zone is present and names the container it could not reach...
    await expect(panel).toContainText(
      'Builds — the erun-dind sidecar every image build runs in',
      withTestBudget(),
    );
    await expect(panel).toContainText('the sidecar did not answer', withTestBudget());
    // ...and carries no figures of its own, so the two meters are the runtime
    // container's: a not-read sidecar must never borrow a bar meaning "measured".
    await expect(panel.getByRole('meter', { name: 'Build CPU' })).toHaveCount(0);
    await expect(panel.getByRole('meter', { name: 'Build memory' })).toHaveCount(0);
    await expect(panel.getByRole('meter')).toHaveCount(2, withTestBudget());

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // The panel's content is the answer to LoadRuntimeUsage, and an assertion on
  // it carries no timeout of its own: `toContainText` has no waitFor
  // equivalent, so it resolves to expect's 10s default rather than the budget
  // this test declares. Under contention a read that is merely slow therefore
  // reds the step with the test's own clock unspent, which is how a loaded
  // machine turns into a failing branch nobody touched. The hold below is
  // deliberately just past that 10s default: the smallest delay that
  // discriminates, so the suite pays seconds here rather than the tens a
  // genuinely loaded machine would.
  //
  // Pre-fix this case reds at exactly 10_000ms with 50s of its own budget
  // unused; with the read pointed at that budget it passes at the read's real
  // arrival. The delay is injected at a named RPC (this spec's own
  // LoadRuntimeUsage stub) rather than by loading the machine, so the
  // reproduction is deterministic on a quiet host -- the same shape
  // sidebar-pom-convergence-budget.spec.ts uses for the POM steps.
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
