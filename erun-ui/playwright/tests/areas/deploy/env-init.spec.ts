import type { Page } from '@playwright/test';

import { test, expect, withTestBudget } from '../../../fixtures/erunApp.js';
import { SEED_TENANT } from '../../../fixtures/seedRoot.js';

// stubDialogCluster makes the env-init dialog behave as if a real cluster were
// reachable: the kubectl stub in the isolated harness reports no contexts (the
// deterministic empty state), so without this the context blocker masks every
// other gate reason. One context lets the dialog auto-select it, and an
// available resource status clears the capacity blocker, leaving the value
// requirements (environment name, container registry) as the only blockers —
// which is exactly what this spec exercises.
// clusterRegistryFailure, when set, makes the LoadClusterRegistry stub return
// this error instead of the endpoint's usual pass-through — used by
// stubDialogClusterWithRegistryFailure below. hostedRegistryAvailable, when
// true, makes the LoadHostedRegistry stub report available instead of the
// harness's default answer (ERUN_HOSTED_REGISTRY_PROBE_OVERRIDE=0 in
// backendEnv(), since page.route cannot intercept the real outbound HTTP call
// underneath that Wails method) — used by
// stubDialogClusterWithHostedRegistryAvailable below. A single page.route
// handler (not two stacked registrations) is required: Playwright runs the
// most-recently registered handler first, and its route.continue() sends the
// request to the real backend rather than falling through to an earlier
// handler, so stacking silently defeated the first stub instead of composing
// with it.
async function stubDialogCluster(
  page: Page,
  clusterRegistryFailure?: string,
  hostedRegistryAvailable?: boolean,
  holdResourceStatusMs?: number,
): Promise<void> {
  let holdUntil = 0;
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string };
    if (body.method === 'LoadKubernetesContexts') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: ['orbstack'] }),
      });
    }
    if (body.method === 'LoadRuntimeResourceStatus') {
      // holdResourceStatusMs makes the capacity reading land a fixed window
      // after the context selection that asked for it, so a step that is
      // merely slow can be told apart from a step that never converges. Held
      // on the first read only -- a refetch is a separate step with its own
      // convergence.
      if (holdResourceStatusMs && holdUntil === 0) {
        holdUntil = Date.now() + holdResourceStatusMs;
      }
      const remaining = holdUntil - Date.now();
      if (remaining > 0) {
        await new Promise((resolve) => setTimeout(resolve, remaining));
      }
      // Mirrors the backend's contract: a reading always names the node it came
      // from and carries two readings, each stating which question it answers --
      // the scheduler's (it places by requests) and the worst case's (what is
      // left if everything bursts to its declared limit at once). Neither is a
      // fixed ceiling, so each carries its own live message.
      const schedulable = {
        cpu: { total: 8, used: 0, free: 8, unit: 'cores', formatted: '8', floored: false },
        memory: {
          total: 16,
          used: 0,
          free: 16,
          unit: 'GiB',
          formatted: '16.0 GiB',
          floored: false,
        },
        message:
          'Right now on node-a (the emptiest node): the scheduler can admit 8 CPU and 16.0 GiB memory more.',
      };
      const worstCase = {
        cpu: { total: 8, used: 2, free: 6, unit: 'cores', formatted: '6', floored: false },
        memory: {
          total: 16,
          used: 4,
          free: 12,
          unit: 'GiB',
          formatted: '12.0 GiB',
          floored: false,
        },
        message:
          'Worst case, with every container on node-a at its declared limit at once: 6 CPU and 12.0 GiB memory left.',
      };
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({
          data: {
            kubernetesContext: 'orbstack',
            available: true,
            node: 'node-a',
            floored: false,
            measuredUsage: true,
            schedulable,
            schedulableComplete: true,
            worstCase,
            nodes: [{ name: 'node-a', schedulable, schedulableComplete: true, worstCase }],
          },
        }),
      });
    }
    if (body.method === 'LoadClusterRegistry' && clusterRegistryFailure) {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ error: clusterRegistryFailure }),
      });
    }
    if (body.method === 'LoadHostedRegistry' && hostedRegistryAvailable) {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: { host: 'registry.erunpaas.com', available: true } }),
      });
    }
    await route.continue();
  });
}

// stubDialogClusterWithRegistryFailure makes a context resolve and its
// capacity load, but the cluster-registry probe (LoadClusterRegistry) fail —
// a VPN hiccup or an RBAC gap, not a real "no registry deployed" answer. This
// used to be a bare `catch { ... }` that silently reset
// clusterRegistry/useClusterRegistry with no error surfaced anywhere,
// indistinguishable from "no in-cluster registry found". The dialog must show
// the probe failure instead.
async function stubDialogClusterWithRegistryFailure(page: Page): Promise<void> {
  await stubDialogCluster(page, 'CLUSTER_REGISTRY_PROBE_UNREACHABLE_MARKER');
}

// stubDialogClusterWithHostedRegistryAvailable is the one route a spec needs
// to exercise the "available" branch of the hosted-registry gate.
async function stubDialogClusterWithHostedRegistryAvailable(page: Page): Promise<void> {
  await stubDialogCluster(page, undefined, true);
}

test.describe('environment init dialog', () => {
  test('opens with tenant pre-populated and cancels', async ({ app }) => {
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();
    await expect(app.envInitDialog.locator()).toBeVisible();

    const tenantInput = app.envInitDialog.tenantInput();
    await expect(tenantInput).toBeVisible();
    expect((await tenantInput.inputValue()).trim()).toBe(SEED_TENANT);

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  test('marks mandatory fields with a required indicator (not colour-only)', async ({
    app,
    page,
  }) => {
    // The reported gap: Create sat disabled with no on-field cue for which values
    // were mandatory — only a one-at-a-time footer reason. Every required field now
    // carries a marker, and the requirement folds into the accessible name via the
    // label's visually-hidden "(required)", so it is conveyed non-visually too.
    await stubDialogCluster(page);
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    // getByRole(name) reads the accessible name — the exact surface this test is
    // about: the "(required)" folds in via the sr-only span, while the visible
    // "*" glyph (aria-hidden) is excluded. getByLabel matches raw label
    // textContent, which includes that glyph ("Tenant* (required)"), so it is the
    // wrong query for an accessible-name assertion.
    for (const name of [
      'Tenant (required)',
      'Environment (required)',
      'Environment type (required)',
      'Kubernetes context (required)',
      'Container registry (required)',
    ]) {
      await expect(page.getByRole('combobox', { name, exact: true })).toBeVisible();
    }

    // The visible marker is a glyph, present in the label DOM.
    await expect(page.locator('label[for="environment-container-registry"]')).toContainText('*');

    // Optional fields (e.g. Runtime version) carry no requirement marker.
    await expect(page.getByLabel('Runtime version (required)', { exact: true })).toHaveCount(0);

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  test('init mode shows the "create" description and a submit-reason status line', async ({
    app,
  }) => {
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    // Two copy branches (pre-populated vs blank) both carry "Create", so match loosely.
    const dialog = app.envInitDialog.locator();
    await expect(dialog.getByText(/Create|create/).first()).toBeVisible();

    // The live region stays mounted even with no blockers, so a blocking reason can
    // appear without a layout shift.
    const reason = app.page.locator('#environment-dialog-submit-reason');
    await expect(reason).toHaveCount(1);

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  test('"Create tenant DevOps repository" checkbox is gone — value is derived from tenant state', async ({
    app,
    page,
  }) => {
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    await expect(page.locator('#environment-bootstrap')).toHaveCount(0);
    await expect(page.locator('#environment-default-tenant')).toBeVisible();
    await expect(page.locator('#environment-no-git')).toBeVisible();

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  test('local-agent type reveals the Local repo path field and hides the no-Git toggle', async ({
    app,
    page,
  }) => {
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    // The control's accessible name carries the folded-in "(required)"; match it
    // by role+name rather than getByLabel (which sees the raw label textContent,
    // including the aria-hidden "*").
    const typeSelect = page.getByRole('combobox', { name: 'Environment type (required)' });
    const localRepoPathInput = page.locator('#environment-local-repo-path');
    const browseButton = page.getByRole('button', { name: /Browse/ });
    const noGitCheckbox = page.locator('#environment-no-git');

    await expect(typeSelect).toBeVisible();
    await expect(localRepoPathInput).toHaveCount(0);
    await expect(browseButton).toHaveCount(0);
    await expect(noGitCheckbox).toBeVisible();

    // The no-Git toggle is hidden for local-agent because it does not affect that
    // init path (see EnvironmentCreateChecks).
    await typeSelect.click();
    await page.getByRole('option', { name: 'Local agent' }).click();
    await expect(localRepoPathInput).toBeVisible();
    await expect(browseButton).toBeVisible();
    await expect(noGitCheckbox).toHaveCount(0);

    await typeSelect.click();
    await page.getByRole('option', { name: 'Runtime' }).click();
    await expect(localRepoPathInput).toHaveCount(0);
    await expect(browseButton).toHaveCount(0);
    await expect(noGitCheckbox).toBeVisible();

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  test('kube-context dropdown survives an environments-changed Wails event', async ({
    app,
    page,
  }) => {
    // Regression guard: an environments-changed tick used to wipe the kube-context
    // dropdown because the Go uiState never carried that field. Firing the event
    // while the dialog is open must leave the same surface visible. The harness's
    // kubectl stub reports no contexts, so that surface is the empty state, never a
    // populated select.
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    const select = page.locator('#environment-kubernetes-context');
    const emptyState = app.envInitDialog
      .locator()
      .getByText('No Kubernetes contexts found')
      .first();

    await expect(emptyState).toBeVisible();
    await expect(select).toBeHidden();

    await page.evaluate(() => {
      const runtime = (
        window as unknown as { runtime: { EventsEmit: (n: string, ...a: unknown[]) => void } }
      ).runtime;
      runtime.EventsEmit('environments-changed');
    });

    await expect(emptyState).toBeVisible();
    await expect(select).toBeHidden();

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  test('keeps Create disabled with a visible reason until every required value is provided', async ({
    app,
    page,
  }) => {
    // Regression (the reported bug): the submit gate only checked that the
    // Kubernetes-context *list* was non-empty — never that a context was actually
    // selected or a container registry chosen. A context can be available yet
    // unselected (the reported state), so the button looked active, but clicking
    // hit the invalid-selection branch whose only feedback was a native validity
    // bubble the Wails WebView does not render — Create silently did nothing. The
    // gate now derives from the same required-field rules submit enforces and
    // surfaces the first missing value, walking the fields as the user fills them.
    await stubDialogCluster(page);
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    const createButton = app.envInitDialog.createButton();
    const reason = app.envInitDialog.submitReason();

    // The stub populates one context but the dialog does not preselect it — the
    // trigger sits on its placeholder, reproducing the reported state. The
    // load itself is the first thing to converge on: asserted after the
    // trigger has resolved, "No Kubernetes contexts found" is absent because
    // the list arrived, not because nothing had been read yet. Every read
    // below is the answer to a Kubernetes-context or capacity round trip, so
    // each waits on the budget this test declares rather than expect's 10s
    // default — see the held-read case at the end of this file.
    await expect(app.envInitDialog.kubernetesContextTrigger()).toContainText(
      'Select Kubernetes context',
      withTestBudget(),
    );
    await expect(app.envInitDialog.locator().getByText('No Kubernetes contexts found')).toHaveCount(
      0,
    );

    // Empty environment name is the first blocker; Create is disabled and says why.
    await expect(createButton).toBeDisabled();
    await expect(reason).toHaveText('Enter a tenant and environment name.');

    // Name provided, context still unselected: the button stays disabled and now
    // names the missing selection instead of silently doing nothing on click.
    await app.envInitDialog.fillEnvironment('review');
    await expect(createButton).toBeDisabled();
    await expect(reason).toHaveText('Select a Kubernetes context.', withTestBudget());

    // Selecting the context advances to the container registry — the required
    // field the old gate never checked, which is what left Create wrongly active.
    await app.envInitDialog.selectKubernetesContext('orbstack');
    await expect(createButton).toBeDisabled();
    await expect(reason).toHaveText('Select a container registry.', withTestBudget());

    // Providing the registry clears the last value blocker, so Create activates
    // and the reason line goes quiet.
    await app.envInitDialog.fillContainerRegistry('ghcr.io/sophium');
    await expect(createButton).toBeEnabled(withTestBudget());
    await expect(reason).toHaveText('', withTestBudget());

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  test('runtime capacity is shown only after a Kubernetes context is selected', async ({
    app,
    page,
  }) => {
    // Reported bug: the RUNTIME RESOURCES panel showed a capacity reading
    // ("Right now on <node> …") while the context dropdown still sat on its
    // "Select Kubernetes context" placeholder — capacity for a cluster the user
    // never chose. The
    // dialog no longer auto-resolves contexts[0] for the capacity fetch, so
    // capacity appears only for an explicitly selected context.
    await stubDialogCluster(page);
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    const capacity = app.envInitDialog.locator().getByText(/Right now on /);

    // One context is available but not preselected — so no capacity is shown.
    await expect(app.envInitDialog.kubernetesContextTrigger()).toContainText(
      'Select Kubernetes context',
      withTestBudget(),
    );
    await expect(capacity).toHaveCount(0);

    // Selecting the context fetches and reveals its capacity: a state
    // transition the dialog reaches from the capacity read, so it converges on
    // that state rather than racing the render.
    await app.envInitDialog.selectKubernetesContext('orbstack');
    await capacity.waitFor({ state: 'visible' });

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  test('does not pre-check default-tenant and offers a skip-Git-checkout option', async ({
    app,
    page,
  }) => {
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    // "Set as default tenant" is OFF by default — creating an environment must not
    // silently repoint the operator's default tenant.
    await expect(page.locator('#environment-default-tenant')).not.toBeChecked();

    // A remote-agent (the default type) offers skipping the Git checkout, off by
    // default and toggleable — the way to create without the remote-worktree flow.
    const skipGit = page.locator('#environment-no-git');
    await expect(skipGit).toBeVisible();
    await expect(skipGit).not.toBeChecked();
    await skipGit.click();
    await expect(skipGit).toBeChecked();

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  // erun#1217: VersionNotices used to render inside the version popover,
  // whose open state required suggestions.length > 0 — so a listing failure
  // (zero suggestions, one notice) computed the exact recovery advice and
  // then put it in a surface that could structurally never open.
  test('shows the version recovery advice even when the popover has nothing to list', async ({
    app,
    page,
  }) => {
    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as { method: string };
      if (body.method === 'LoadVersionSuggestions') {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({
            data: {
              suggestions: [],
              notices: [{ image: 'ghcr.io/acme/erun-devops', kind: 'auth' }],
            },
          }),
        });
      }
      await route.continue();
    });

    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    // Visible directly, without ever opening the (now correctly disabled,
    // nothing-to-pick) popover. The notices are the answer to the
    // LoadVersionSuggestions read, so they converge on arriving rather than on
    // expect's 10s default.
    await expect(app.envInitDialog.versionChoicesButton()).toBeDisabled(withTestBudget());
    const notices = app.envInitDialog.versionNotices();
    await notices.waitFor({ state: 'visible' });
    await expect(notices).toContainText('ghcr.io/acme/erun-devops is private', withTestBudget());
    await expect(notices).toContainText('docker login ghcr.io', withTestBudget());

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  // erun#1217: the in-app route that unblocks a user with no local kubectl
  // context (Settings → Cloud aliases → Add AWS account → Cloud contexts →
  // Init provisions a managed cluster) was never named on this screen.
  test('names the managed-cloud-cluster route when no Kubernetes contexts are found', async ({
    app,
  }) => {
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    const emptyState = app.envInitDialog.locator().getByText('No Kubernetes contexts found');
    await expect(emptyState).toBeVisible();
    const body = app.envInitDialog.locator();
    await expect(body).toContainText('Cloud aliases');
    await expect(body).toContainText('Cloud contexts');

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  // erun#1217: this dialog IS the documented Windows getting-started path
  // (erun-docs/docs/getting-started/first-environment.md), but its recovery
  // copy assumed macOS (~/.zshenv, brew) unconditionally.
  test.describe('on a Windows user agent', () => {
    test.use({
      userAgent:
        'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36',
    });

    test('names Windows recovery steps instead of macOS-only ones', async ({ app }) => {
      await app.sidebar.openInitDialog();
      await app.envInitDialog.waitForOpen();

      const body = app.envInitDialog.locator();
      await expect(body).toContainText('winget install');
      await expect(body).not.toContainText('brew install');
      await expect(body).not.toContainText('.zshenv');

      await app.envInitDialog.cancel();
      await app.envInitDialog.waitForClosed();
    });
  });

  // erun#1217: a fresh install's container registry field offered no
  // placeholder, no helper, and no suggestions — nothing to go on for a
  // required free-text value.
  test('explains the container registry format and the auto-detect route when nothing is detected', async ({
    app,
    page,
  }) => {
    await stubDialogCluster(page);
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    // stubDialogCluster resolves a context but the harness's kubectl stub
    // cannot reach a real cluster, so no in-cluster registry is detected —
    // the "nothing to go on" state this fix targets.
    await app.envInitDialog.selectKubernetesContext('orbstack');
    await expect(page.getByText('Use in-cluster registry', { exact: false })).toHaveCount(0);

    const help = app.envInitDialog.locator().getByText(/Where images push to and pull from/);
    await expect(help).toBeVisible();
    await expect(help).toContainText('ghcr.io/your-org');
    await expect(help).toContainText('Cloud aliases');

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  test('a cluster-registry probe failure shows the failure, not a silent reset (#1212)', async ({
    app,
    page,
  }) => {
    await stubDialogClusterWithRegistryFailure(page);
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    // Selecting the context fires refreshDialogClusterRegistry, which fails.
    await app.envInitDialog.selectKubernetesContext('orbstack');

    // The failure is the probe's own answer arriving after the selection.
    await expect(app.envInitDialog.locator().getByRole('alert')).toContainText(
      'CLUSTER_REGISTRY_PROBE_UNREACHABLE_MARKER',
      withTestBudget(),
    );

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  // The dialog's reads are answers to round trips this spec's own stub owns,
  // and an assertion on one carries no timeout of its own: `toContainText`,
  // `toBeEnabled` and a bare `toBeVisible` have no waitFor equivalent that
  // outlives expect's default, so each resolved to a 10s clock the test never
  // declared. Under contention a read that is merely slow therefore reds the
  // test with its own clock unspent, which is how a loaded builder turns a
  // green branch red. The hold below is deliberately just past that 10s
  // default: the smallest delay that discriminates, so the suite pays seconds
  // here rather than the tens a genuinely loaded machine would. It is injected
  // at LoadRuntimeResourceStatus -- the read this step waits on -- rather than
  // by loading the machine, so the reproduction is deterministic on a quiet
  // host.
  //
  // Pre-fix this case reds at exactly 10_000ms with 50s of its own budget
  // unused; converged on the state the read produces, it passes when the
  // reading actually lands.
  test('a capacity reading that lands past the step cap is waited out, not cut off', async ({
    app,
    page,
  }) => {
    test.setTimeout(60_000);
    await stubDialogCluster(page, undefined, undefined, 12_000);
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    const capacity = app.envInitDialog.locator().getByText(/Right now on /);
    await app.envInitDialog.selectKubernetesContext('orbstack');
    await capacity.waitFor({ state: 'visible' });
    await expect(capacity).toContainText('Right now on node-a', withTestBudget());

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  test('offers the hosted registry only once it is confirmed reachable', async ({ app, page }) => {
    // The reported defect: the hosted-registry option was offered
    // unconditionally, with no check that registry.erunpaas.com was actually
    // reachable, unlike the in-cluster registry which is already gated on
    // clusterRegistry?.deployed. The harness's default answer (no stub) is
    // "unavailable" — the same answer this host gets in real life — so the
    // checkbox must stay disabled and explain why, and picking a different
    // registry must still be possible.
    await stubDialogCluster(page);
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    // The checkbox's state is the hosted-registry probe's answer — a real
    // outbound call, the slowest read on this dialog — so it waits on the
    // budget this test declares rather than expect's 10s default.
    const hosted = app.envInitDialog.hostedRegistryCheckbox();
    await hosted.waitFor({ state: 'visible' });
    await expect(hosted).toBeDisabled(withTestBudget());
    await expect(app.envInitDialog.locator()).toContainText('does not resolve', withTestBudget());

    await app.envInitDialog.selectKubernetesContext('orbstack');
    await app.envInitDialog.fillEnvironment('review');
    await app.envInitDialog.fillContainerRegistry('ghcr.io/sophium');
    await expect(app.envInitDialog.createButton()).toBeEnabled(withTestBudget());

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  test('once reachable, selecting the hosted registry clears the container-registry requirement', async ({
    app,
    page,
  }) => {
    await stubDialogClusterWithHostedRegistryAvailable(page);
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    // Enabled here is the same probe's answer, this time a reachable one.
    const hosted = app.envInitDialog.hostedRegistryCheckbox();
    await expect(hosted).toBeEnabled(withTestBudget());

    await app.envInitDialog.selectKubernetesContext('orbstack');
    await app.envInitDialog.fillEnvironment('review');
    // No container registry is filled in — the hosted registry needs none.
    await expect(app.envInitDialog.createButton()).toBeDisabled(withTestBudget());

    await hosted.click();
    await expect(hosted).toBeChecked(withTestBudget());
    await expect(app.envInitDialog.createButton()).toBeEnabled(withTestBudget());
    await expect(app.envInitDialog.submitReason()).toHaveText('', withTestBudget());

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });
});
