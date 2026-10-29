import type { Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_ENV_BETA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// With a cross-env session focused, the diff panel shows one section per linked
// env, each with its own loading/error/scope state.

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

// `scope`/`selectedCommit` are echoed back because the frontend re-derives the
// slot's scope from the response.
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
    // Empty: a fictional directory would add a failing section to every test here.
    directories: [],
    sessionId: RUNNING_SESSION_ID,
    status: 'running',
    busy: false,
    transient: false,
    shellRunning: false,
    shellCommand: '',
    shellStartedAtUnix: 0,
  };
}

// LoadDiff resolves per env from `byEnv`; anything else falls through to the
// real backend. `calls` records each LoadDiff target env in order.
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
  // These cases share app state across the same seeded envs, so they must run
  // in order in one worker.
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
    await app.reviewPanel.waitForOpen();
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
    await app.reviewPanel.waitForOpen();
    const review = app.reviewPanel;
    await expect
      .poll(() => review.diffSectionPaths())
      .toEqual(expect.arrayContaining(['alpha-only.ts', 'beta-only.ts']));

    // The raw "pw/alpha" envKey never reaches the DOM.
    await expect(review.envLabels(ALPHA_ENV_KEY)).toHaveCount(3);
    await expect(review.envLabels(BETA_ENV_KEY)).toHaveCount(3);
    await expect(review.changedFilesEnvLabel(ALPHA_ENV_KEY)).toBeVisible();
    await expect(review.envSectionHeader(ALPHA_ENV_KEY)).toBeVisible();
    await expect(page.getByText(ALPHA_ENV_KEY, { exact: true })).toHaveCount(0);
  });

  test('a single linked environment renders no redundant env label', async ({ app }) => {
    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);
    await app.titlebar.toggleReviewPanel();
    // Otherwise a zero count passes when the panel never opened.
    await app.reviewPanel.waitForOpen();

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
    await app.reviewPanel.waitForOpen();
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
    await app.reviewPanel.waitForOpen();
    const review = app.reviewPanel;
    // The changed-files tree renders its own copy of the alert.
    await review.collapseChangedFilesSection();
    await expect(review.changedFilesTree()).toBeHidden();

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
    await app.reviewPanel.waitForOpen();
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
    // alpha falls through to the real backend and never resolves; its section must
    // render its own empty/error state, never beta's file.
    await stubOrchestratorAndDiffs(page, {
      [SEED_ENV_BETA]: { kind: 'success', file: 'beta-only.ts' },
    });
    await app.reboot();

    await app.sidebar.openOrchestratorSession(ORCHESTRATOR_ID);
    await app.titlebar.toggleReviewPanel();
    await app.reviewPanel.waitForOpen();
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
    await app.reviewPanel.waitForOpen();
    const review = app.reviewPanel;
    await expect
      .poll(() => review.diffSectionPaths())
      .toEqual(expect.arrayContaining(['alpha.ts', 'beta.ts']));

    // Controls render in the orchestrator's configured env order.
    const alphaAll = review.reviewBoundaryButton('All branch changes', 0);
    const alphaCurrent = review.reviewBoundaryButton('Current local changes', 0);
    const betaAll = review.reviewBoundaryButton('All branch changes', 1);
    const betaCurrent = review.reviewBoundaryButton('Current local changes', 1);

    // The range controls live in a separate subtree from DiffList and may mount later.
    await expect(betaCurrent).toBeVisible();
    await expect(betaAll).toBeVisible();

    await expect(alphaCurrent).toHaveAttribute('aria-pressed', 'true');
    await expect(betaCurrent).toHaveAttribute('aria-pressed', 'true');

    // A scope change reloads every linked env's diff, so wait for both round trips
    // before reading button state.
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
    await app.titlebar.changedFilesToggle().waitFor({ state: 'visible' });
    await expect(app.titlebar.changedFilesToggle()).toBeVisible();
    // The orchestrator session, not the diff panel, gates these controls.
    await expect(app.titlebar.vscodeButton()).toHaveCount(0);
    await expect(app.titlebar.intellijButton()).toHaveCount(0);
    await expect(app.titlebar.contributeToggleButton()).toHaveCount(0);

    await app.titlebar.toggleReviewPanel();
    await app.titlebar.changedFilesToggle().waitFor({ state: 'hidden' });
    await expect(app.titlebar.changedFilesToggle()).toHaveCount(0);
  });
});
