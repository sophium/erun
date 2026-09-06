import type { Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_ENV_BETA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// The orchestrator cross-env diff panel (#1178) is the orchestrator's primary
// code-review instrument: with a cross-env session focused, the panel shows
// one section per LINKED environment instead of one merged diff, and each
// section owns its own loading/error/scope state so one stopped environment
// cannot blank another's. None of the existing diff specs (review-diff-tree,
// diff-error-copy, erun-section) exercise a running multi-env orchestrator
// session, so this spec fills that gap.

const ORCHESTRATOR_ID = 'pw-orch-diff';
const RUNNING_SESSION_ID = 9001;
const ALPHA_ENV_KEY = `${SEED_TENANT}/${SEED_ENV_ALPHA}`;
const BETA_ENV_KEY = `${SEED_TENANT}/${SEED_ENV_BETA}`;

interface DiffLineStub {
  kind: string;
  oldLine: number | null;
  newLine: number | null;
  content: string;
}
interface DiffFileStub {
  path: string;
  status: string;
  additions: number;
  deletions: number;
  binary: boolean;
  hunks: { header: string; lines: DiffLineStub[] }[];
}
interface DiffCommitStub {
  hash: string;
  shortHash: string;
  subject: string;
  author: string;
  date: string;
}
interface DiffReviewBaseStub {
  branch: string;
  commit: string;
  shortCommit: string;
}

function diffFile(path: string): DiffFileStub {
  return {
    path,
    status: 'modified',
    additions: 1,
    deletions: 0,
    binary: false,
    hunks: [
      {
        header: '@@ -1,1 +1,1 @@',
        lines: [{ kind: 'add', oldLine: null, newLine: 1, content: `${path}:0` }],
      },
    ],
  };
}

// One success stub per environment: a single distinct file, plus optional
// review-range state (base + commits) for the scope-isolation case. The
// `scope`/`selectedCommit` echoed back mirror the real backend contract
// (applyReviewDiffSuccess re-derives the slot's scope/commit from the
// response), so a spec that selects a scope must see it round-trip.
interface EnvDiffSuccessStub {
  kind: 'success';
  file: string;
  reviewBase?: DiffReviewBaseStub;
  reviewCommits?: DiffCommitStub[];
}
interface EnvDiffErrorStub {
  kind: 'error';
  message: string;
}
type EnvDiffStub = EnvDiffSuccessStub | EnvDiffErrorStub;

function orchestratorSnapshot(): unknown {
  return {
    id: ORCHESTRATOR_ID,
    name: ORCHESTRATOR_ID,
    environments: [
      { tenant: SEED_TENANT, environment: SEED_ENV_ALPHA, directory: '/tmp/orch-alpha' },
      { tenant: SEED_TENANT, environment: SEED_ENV_BETA, directory: '/tmp/orch-beta' },
    ],
    tenants: [SEED_TENANT],
    directories: ['/tmp/orch-alpha', '/tmp/orch-beta'],
    sessionId: RUNNING_SESSION_ID,
    status: 'running',
    busy: false,
    transient: false,
    shellRunning: false,
    shellCommand: '',
    shellStartedAtUnix: 0,
  };
}

// stubOrchestratorAndDiffs combines both fakes behind one route registration:
// ListOrchestrators reports the running, multi-env orchestrator above, and
// LoadDiff resolves per environment according to `byEnv`, keyed by
// environment name. Everything else (and any environment missing from
// `byEnv`) falls through to the real headless backend. `calls` records every
// LoadDiff request's target environment, in order, so a spec can assert one
// fetch happened per linked environment rather than one merged fetch.
async function stubOrchestratorAndDiffs(
  page: Page,
  byEnv: Record<string, EnvDiffStub>,
): Promise<{ calls: string[] }> {
  const calls: string[] = [];
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as {
      method?: string;
      args?: unknown[];
    };
    if (body.method === 'ListOrchestrators') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: [orchestratorSnapshot()] }),
      });
    }
    if (body.method !== 'LoadDiff') {
      await route.continue();
      return;
    }
    const [selection, options] = (body.args ?? []) as [
      { tenant: string; environment: string },
      { scope?: string; selectedCommit?: string } | undefined,
    ];
    calls.push(selection.environment);
    const stub = byEnv[selection.environment];
    if (!stub) {
      await route.continue();
      return;
    }
    if (stub.kind === 'error') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ error: stub.message }),
      });
    }
    return route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({
        data: {
          rawDiff: '',
          workingDirectory: '/seed',
          summary: { fileCount: 1, additions: 1, deletions: 0 },
          files: [diffFile(stub.file)],
          tree: [{ name: stub.file, path: stub.file, type: 'file', depth: 0 }],
          reviewBase: stub.reviewBase,
          reviewCommits: stub.reviewCommits,
          scope: options?.scope ?? 'current',
          selectedCommit: options?.selectedCommit ?? '',
          includesWorktree: true,
        },
      }),
    });
  });
  return { calls };
}

test.describe('orchestrator cross-env diff panel (#1178)', () => {
  // These cases drive the same two seeded environments (alpha and beta) and the
  // app state each leaves behind, so they are order-dependent within a worker.
  // Under fullyParallel a different subset lands in each worker and that
  // dependence surfaces as an empty diff. Keep them together, in order, in one
  // worker; the rest of the suite still parallelises around them.
  test.describe.configure({ mode: 'serial' });

  test('renders one section per linked environment, not a single merged diff', async ({
    app,
    page,
  }) => {
    await stubOrchestratorAndDiffs(page, {
      [SEED_ENV_ALPHA]: { kind: 'success', file: 'alpha-only.ts' },
      [SEED_ENV_BETA]: { kind: 'success', file: 'beta-only.ts' },
    });
    await app.reboot();

    await app.sidebar.openOrchestratorSession(ORCHESTRATOR_ID);
    await app.titlebar.toggleReviewPanel();
    const review = app.reviewPanel;

    await expect(review.envSectionHeader(ALPHA_ENV_KEY)).toBeVisible();
    await expect(review.envSectionHeader(BETA_ENV_KEY)).toBeVisible();
    await expect
      .poll(() => review.diffSectionPaths())
      .toEqual(expect.arrayContaining(['alpha-only.ts', 'beta-only.ts']));
  });

  test('the review-layers block, changed-files tree, and diff panel share one env label treatment (#1314)', async ({
    app,
    page,
  }) => {
    const base: DiffReviewBaseStub = { branch: 'main', commit: 'base0', shortCommit: 'base0' };
    await stubOrchestratorAndDiffs(page, {
      [SEED_ENV_ALPHA]: {
        kind: 'success',
        file: 'alpha-only.ts',
        reviewBase: base,
        reviewCommits: [
          { hash: 'a1', shortHash: 'a1', subject: 'Alpha commit', author: 'a', date: '2024-01-01' },
        ],
      },
      [SEED_ENV_BETA]: { kind: 'success', file: 'beta-only.ts', reviewBase: base },
    });
    await app.reboot();

    await app.sidebar.openOrchestratorSession(ORCHESTRATOR_ID);
    await app.titlebar.toggleReviewPanel();
    const review = app.reviewPanel;
    await expect
      .poll(() => review.diffSectionPaths())
      .toEqual(expect.arrayContaining(['alpha-only.ts', 'beta-only.ts']));

    // Three surfaces render "pw / alpha" (review layers, changed-files tree,
    // diff panel section header) — the raw "pw/alpha" envKey never reaches
    // the DOM, and every occurrence uses the same spaced, app-native format.
    await expect(review.envLabels(ALPHA_ENV_KEY)).toHaveCount(3);
    await expect(review.envLabels(BETA_ENV_KEY)).toHaveCount(3);
    await expect(review.changedFilesEnvLabel(ALPHA_ENV_KEY)).toBeVisible();
    await expect(review.envSectionHeader(ALPHA_ENV_KEY)).toBeVisible();
    await expect(page.getByText(ALPHA_ENV_KEY, { exact: true })).toHaveCount(0);
  });

  test('a single linked environment renders no redundant env label', async ({ app }) => {
    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);
    await app.titlebar.toggleReviewPanel();

    // Single-env sessions must not gain the multi-env label (#1314) — the
    // review panel here has exactly one target, so no surface names it.
    await expect(app.reviewPanel.envLabels(ALPHA_ENV_KEY)).toHaveCount(0);
  });

  test('fetches each linked environment independently rather than one shared request', async ({
    app,
    page,
  }) => {
    const { calls } = await stubOrchestratorAndDiffs(page, {
      [SEED_ENV_ALPHA]: { kind: 'success', file: 'alpha-only.ts' },
      [SEED_ENV_BETA]: { kind: 'success', file: 'beta-only.ts' },
    });
    await app.reboot();

    await app.sidebar.openOrchestratorSession(ORCHESTRATOR_ID);
    await app.titlebar.toggleReviewPanel();
    await expect
      .poll(() => app.reviewPanel.diffSectionPaths())
      .toEqual(expect.arrayContaining(['alpha-only.ts', 'beta-only.ts']));

    await expect
      .poll(() => calls.filter((env) => env === SEED_ENV_ALPHA).length)
      .toBeGreaterThan(0);
    await expect.poll(() => calls.filter((env) => env === SEED_ENV_BETA).length).toBeGreaterThan(0);
  });

  test("one environment's failure shows its own error while the other keeps rendering", async ({
    app,
    page,
  }) => {
    await stubOrchestratorAndDiffs(page, {
      [SEED_ENV_ALPHA]: { kind: 'error', message: 'ALPHA_UNREACHABLE_MARKER' },
      [SEED_ENV_BETA]: { kind: 'success', file: 'beta-ok.ts' },
    });
    await app.reboot();

    await app.sidebar.openOrchestratorSession(ORCHESTRATOR_ID);
    await app.titlebar.toggleReviewPanel();
    const review = app.reviewPanel;
    // The changed-files tree renders its own copy of the same per-env alert;
    // collapse it so the alert count below reflects the diff list alone.
    await review.collapseChangedFilesSection();
    await expect(review.changedFilesTree()).toBeHidden();

    // Both sections still render (the failing environment does not disappear
    // or blank the panel), beta's file survives alpha's failure, and exactly
    // one alert shows -- proving alpha's error stayed in its own section
    // instead of clearing beta's diff too.
    await expect(review.envSectionHeader(ALPHA_ENV_KEY)).toBeVisible();
    await expect(review.envSectionHeader(BETA_ENV_KEY)).toBeVisible();
    await expect(review.errorAlerts()).toHaveCount(1);
    await expect(
      review.errorAlerts().filter({ hasText: 'ALPHA_UNREACHABLE_MARKER' }),
    ).toBeVisible();
    await expect.poll(() => review.diffSectionPaths()).toEqual(['beta-ok.ts']);
  });

  test('each environment carries its own distinct error independently', async ({ app, page }) => {
    await stubOrchestratorAndDiffs(page, {
      [SEED_ENV_ALPHA]: { kind: 'error', message: 'ALPHA_UNREACHABLE_MARKER' },
      [SEED_ENV_BETA]: { kind: 'error', message: 'BETA_UNREACHABLE_MARKER' },
    });
    await app.reboot();

    await app.sidebar.openOrchestratorSession(ORCHESTRATOR_ID);
    await app.titlebar.toggleReviewPanel();
    const review = app.reviewPanel;
    await review.collapseChangedFilesSection();
    await expect(review.changedFilesTree()).toBeHidden();

    await expect(review.errorAlerts()).toHaveCount(2);
    await expect(
      review.errorAlerts().filter({ hasText: 'ALPHA_UNREACHABLE_MARKER' }),
    ).toBeVisible();
    await expect(review.errorAlerts().filter({ hasText: 'BETA_UNREACHABLE_MARKER' })).toBeVisible();
  });

  test("a missing/unfetched environment section never shows another environment's files", async ({
    app,
    page,
  }) => {
    // alpha never resolves (falls through to the real backend for an
    // undeployed env, which reports unreachable/no-diff); beta resolves with
    // a distinct file. diffByEnv is a sparse map -- useEnvDiffSlot substitutes
    // the empty slot for a key that hasn't been fetched instead of throwing or
    // aliasing another environment's slot -- so alpha's section must render
    // its own empty/error state, never beta's file.
    await stubOrchestratorAndDiffs(page, {
      [SEED_ENV_BETA]: { kind: 'success', file: 'beta-only.ts' },
    });
    await app.reboot();

    await app.sidebar.openOrchestratorSession(ORCHESTRATOR_ID);
    await app.titlebar.toggleReviewPanel();
    const review = app.reviewPanel;

    await expect(review.envSectionHeader(ALPHA_ENV_KEY)).toBeVisible();
    await expect(review.envSectionHeader(BETA_ENV_KEY)).toBeVisible();
    await expect.poll(() => review.diffSectionPaths()).toEqual(['beta-only.ts']);
  });

  test('per-env review scope selection does not leak across environments', async ({
    app,
    page,
  }) => {
    const base: DiffReviewBaseStub = { branch: 'main', commit: 'base0', shortCommit: 'base0' };
    await stubOrchestratorAndDiffs(page, {
      [SEED_ENV_ALPHA]: {
        kind: 'success',
        file: 'alpha.ts',
        reviewBase: base,
        reviewCommits: [
          { hash: 'a1', shortHash: 'a1', subject: 'Alpha commit', author: 'a', date: '2024-01-01' },
        ],
      },
      [SEED_ENV_BETA]: {
        kind: 'success',
        file: 'beta.ts',
        reviewBase: base,
        reviewCommits: [
          { hash: 'b1', shortHash: 'b1', subject: 'Beta commit', author: 'a', date: '2024-01-01' },
        ],
      },
    });
    await app.reboot();

    await app.sidebar.openOrchestratorSession(ORCHESTRATOR_ID);
    await app.titlebar.toggleReviewPanel();
    const review = app.reviewPanel;
    await expect
      .poll(() => review.diffSectionPaths())
      .toEqual(expect.arrayContaining(['alpha.ts', 'beta.ts']));

    // Index 0 is alpha, index 1 is beta: ReviewRangeControls renders one
    // control per target in selectReviewEnvTargets' order, which mirrors the
    // orchestrator's configured environments order set up above.
    const alphaAll = review.reviewBoundaryButton('All branch changes', 0);
    const alphaCurrent = review.reviewBoundaryButton('Current local changes', 0);
    const betaAll = review.reviewBoundaryButton('All branch changes', 1);
    const betaCurrent = review.reviewBoundaryButton('Current local changes', 1);

    // diffSectionPaths() above only proves the diff panel's file list
    // (DiffList) has rendered both environments' files. The review-layers
    // controls read the same per-env slot but live in a separate subtree
    // (ReviewRangeControls, inside the changed-files aside), so nth(1) has
    // nothing to resolve to until beta's own controls have mounted -- wait
    // for that directly instead of assuming it happens in the same commit.
    await expect(betaCurrent).toBeVisible();
    await expect(betaAll).toBeVisible();

    await expect(alphaCurrent).toHaveAttribute('aria-pressed', 'true');
    await expect(betaCurrent).toHaveAttribute('aria-pressed', 'true');

    // A scope change on ANY linked environment reloads every linked
    // environment's diff (loadReviewDiff fetches the whole target set, not
    // just the one whose scope changed), so alpha's click also sends beta a
    // fresh (same-scope) LoadDiff round trip. Wait on that concrete round
    // trip for both environments -- and re-confirm both files are still in
    // the diff panel -- before reading any button state, rather than racing
    // straight into the attribute check.
    const reloadedBoth = Promise.all(
      [SEED_ENV_ALPHA, SEED_ENV_BETA].map((environment) =>
        page.waitForResponse(
          (response) =>
            response.url().includes('__erun_invoke') &&
            (response.request().postData() ?? '').includes('"LoadDiff"') &&
            (response.request().postData() ?? '').includes(environment),
        ),
      ),
    );
    await alphaAll.click();
    await reloadedBoth;
    await expect
      .poll(() => review.diffSectionPaths())
      .toEqual(expect.arrayContaining(['alpha.ts', 'beta.ts']));

    await expect(alphaAll).toHaveAttribute('aria-pressed', 'true');
    await expect(alphaCurrent).toHaveAttribute('aria-pressed', 'false');
    // beta's own scope selection is untouched by alpha's.
    await expect(betaCurrent).toHaveAttribute('aria-pressed', 'true');
    await expect(betaAll).toHaveAttribute('aria-pressed', 'false');
  });

  test('the titlebar right cluster keeps only the diff toggle in orchestrator mode, plus the changed-files sub-toggle once the panel is open (#1190)', async ({
    app,
    page,
  }) => {
    await stubOrchestratorAndDiffs(page, {
      [SEED_ENV_ALPHA]: { kind: 'success', file: 'alpha-only.ts' },
      [SEED_ENV_BETA]: { kind: 'success', file: 'beta-only.ts' },
    });
    await app.reboot();

    await app.sidebar.openOrchestratorSession(ORCHESTRATOR_ID);

    await expect(app.titlebar.diffPanelToggle()).toBeVisible();
    await expect(app.titlebar.vscodeButton()).toHaveCount(0);
    await expect(app.titlebar.intellijButton()).toHaveCount(0);
    await expect(app.titlebar.contributeToggleButton()).toHaveCount(0);
    await expect(app.titlebar.changedFilesToggle()).toHaveCount(0);

    await app.titlebar.toggleReviewPanel();
    await expect(app.titlebar.changedFilesToggle()).toBeVisible();
    // The env-scoped controls stay hidden even with the panel open -- the
    // orchestrator session, not the diff panel, gates them.
    await expect(app.titlebar.vscodeButton()).toHaveCount(0);
    await expect(app.titlebar.intellijButton()).toHaveCount(0);
    await expect(app.titlebar.contributeToggleButton()).toHaveCount(0);

    await app.titlebar.toggleReviewPanel();
    await expect(app.titlebar.changedFilesToggle()).toHaveCount(0);
  });
});
