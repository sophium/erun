import { execFileSync } from 'node:child_process';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';

import type { Page, Request, Route } from '@playwright/test';

import { test, expect } from '../../../fixtures/erunApp.js';
import { removeOrchestrator } from '../../../fixtures/seedRoot.js';
import type { AppShell } from '../../../pages/index.js';

// An orchestrator may work in directories that belong to no environment. The
// review panel resolved its targets from the orchestrator's LINKED ENVIRONMENTS
// alone, so a definition whose only scope is a directory read "No environment
// selected" — the whole review surface missing for a checkout the orchestrator
// actually works in. This locks in that a directory is a review target: its path
// labels the section, and its diff is read locally with host git rather than
// over an MCP edge it does not have.
test.describe('the diff panel shows a directory the orchestrator works in', () => {
  test('a directory-only orchestrator shows its changed files, and no environment is needed', async ({
    app,
    page,
  }) => {
    // This case stages a real git checkout, creates an orchestrator against it,
    // starts a session and then waits for a host-git diff -- several round trips
    // more than the suite-wide 30s per-test default is sized for, and enough of
    // them for a loaded gate box to outrun it without any single step being
    // wrong. The waits below each name their own owner; this only stops the
    // whole-test clock from being the shortest of them.
    test.setTimeout(60_000);
    const directory = makeChangedGitRepo();
    await stubDirectoryPicker(page, directory);
    const name = 'directory-diff-test';

    try {
      await app.sidebar.newOrchestratorButton().click();
      await app.orchestratorDialog.waitForOpen();
      await app.orchestratorDialog.directoriesAddButton().click();
      await expect(app.orchestratorDialog.directoryRow(directory)).toBeVisible();
      await app.orchestratorDialog.create(name);
      await app.orchestratorDialog.waitForClosed();

      await openOrchestratorReviewPanel(app, name);

      // The section says what it is. A single target renders no header (the panel
      // keeps the env-tab case chrome-free), so the note is what identifies this
      // as a directory section rather than an unlabelled environment one -- and
      // it is also the witness that the panel has SWITCHED to the directory
      // target, which everything below depends on. Turning the session row's
      // click into an active session with a resolved target is a round trip, so
      // converge on the settled state instead of racing it.
      // No timeout of its own: a bare `waitFor` defers to this test's declared
      // 60s, where a fixed 25s would give up with the test's own budget still
      // unspent -- the outer-bound/inner-bound mistake, not a convergence.
      const localDirectoryNote = page.getByText('Local directory — no hosted review');
      await localDirectoryNote.waitFor({ state: 'visible' });

      // The operator's symptom, pinned: this scope is reviewable, not empty.
      // Read now that the panel has settled on this target, so a directory that
      // never resolved cannot pass this by rendering nothing at all.
      await expect(page.getByText('No environment selected')).toHaveCount(0);

      // And the changed file arrived, which is the directory's own diff having
      // been read with host git -- the whole point of the target existing. The
      // panel loads its diffs when it OPENS, and it was already open when the
      // session became active, so this spec asks for the fetch explicitly
      // rather than waiting out the periodic refresh. Re-issue that ask on each
      // attempt: one ask that lands before the panel has switched targets
      // fetches nothing, and nothing retries it, so a single click would leave
      // this waiting on a slot that was never fetched.
      // The bare toPass defers to this test's declared 60s; the 2s poll inside
      // it is a per-attempt probe, deliberately short so one attempt that lands
      // before the panel has switched targets costs a retry rather than the
      // whole convergence window.
      await expect(async () => {
        await app.reviewPanel.refreshDiff();
        await expect
          .poll(() => app.reviewPanel.treeFilePaths(), { timeout: 2_000 })
          .toContain('notes.md');
      }).toPass();
    } finally {
      removeOrchestrator(name);
      fs.rmSync(directory, { recursive: true, force: true });
    }
  });

  // The host-git diff this target reads is a round trip, and the two waits
  // above used to carry fixed bounds (25s) shorter than the 60s this test
  // declares: a read merely slower than 25s reddened them with a third of the
  // test's own budget unspent. Both now defer to that budget.
  //
  // The hold below is deliberately just past the caps they used to carry --
  // 27s, inside the 60s declared. It is injected at LoadDiff rather than by
  // loading the machine, and it continues to the real backend afterwards, so
  // the diff under test is still read with host git; only its arrival is
  // delayed. Pre-fix this case reds at exactly 25_000ms with 35s unused.
  test('a directory diff that lands past the step cap is waited out, not cut off', async ({
    app,
    page,
  }) => {
    // 90s, not the 60s the case above declares: the hold below has to exceed
    // the 25s step cap it replaces (27s) to discriminate at all, and that delay
    // is on top of a setup that stages a real checkout, an orchestrator and a
    // session. Budget sized around the bound this case declares, the same way
    // the sibling held-read cases are.
    test.setTimeout(90_000);
    const directory = makeChangedGitRepo();
    const name = 'directory-diff-hold-test';
    // One handler rather than stubDirectoryPicker plus a second route: this
    // suite's routes do not chain, so the picker stub and the hold have to be
    // the same registration or one of them would swallow the other's method.
    let holdUntil = 0;
    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as { method: string };
      if (body.method === 'ChooseLocalRepoPath') {
        await route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: directory }),
        });
        return;
      }
      if (body.method === 'LoadDirectoryDiff') {
        if (holdUntil === 0) {
          holdUntil = Date.now() + 27_000;
        }
        const remaining = holdUntil - Date.now();
        if (remaining > 0) {
          await new Promise((resolve) => setTimeout(resolve, remaining));
        }
      }
      await route.continue();
    });

    try {
      await app.sidebar.newOrchestratorButton().click();
      await app.orchestratorDialog.waitForOpen();
      await app.orchestratorDialog.directoriesAddButton().click();
      await expect(app.orchestratorDialog.directoryRow(directory)).toBeVisible();
      await app.orchestratorDialog.create(name);
      await app.orchestratorDialog.waitForClosed();

      await openOrchestratorReviewPanel(app, name);

      // No timeout of its own: a bare `waitFor` defers to this test's declared
      // 60s, where a fixed 25s would give up with the test's own budget still
      // unspent. The bare toPass below defers the same way; the 2s poll inside
      // it is a per-attempt probe, deliberately short so one attempt that lands
      // before the panel has switched targets costs a retry rather than the
      // whole convergence window.
      const localDirectoryNote = page.getByText('Local directory — no hosted review');
      await localDirectoryNote.waitFor({ state: 'visible' });

      await expect(async () => {
        await app.reviewPanel.refreshDiff();
        await expect
          .poll(() => app.reviewPanel.treeFilePaths(), { timeout: 2_000 })
          .toContain('notes.md');
      }).toPass();
    } finally {
      removeOrchestrator(name);
      fs.rmSync(directory, { recursive: true, force: true });
    }
  });
});

// openOrchestratorReviewPanel makes the orchestrator's session active and
// ensures the review panel is open. The panel resolves its targets from the
// ACTIVE orchestrator session, so the session has to be open before the panel
// has anything to show.
async function openOrchestratorReviewPanel(app: AppShell, name: string): Promise<void> {
  await app.sidebar.openOrchestratorSession(name);
  if (!(await app.reviewPanel.isOpen())) {
    await app.titlebar.toggleReviewPanel();
    // This branch is the one that knows the panel was closed, so the toggle
    // can only have opened it -- converge here rather than leaving the
    // refresh below to race the panel's own render.
    await app.reviewPanel.waitForOpen();
  }
}

// stubDirectoryPicker answers the native picker the way tenant-dashboard-*.spec.ts
// stubs a backend read: it cannot be driven from a browser.
async function stubDirectoryPicker(page: Page, directory: string): Promise<void> {
  await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string };
    if (body.method === 'ChooseLocalRepoPath') {
      await route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: directory }),
      });
      return;
    }
    await route.continue();
  });
}

// makeChangedGitRepo is a real checkout with one uncommitted change, because the
// diff under test is host git reading this directory — the same way the operator's
// own directory is read.
function makeChangedGitRepo(): string {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'erun-directory-diff-'));
  const git = (...args: string[]): void => {
    execFileSync('git', ['-C', directory, ...args], { stdio: 'pipe' });
  };
  git('init');
  git('config', 'user.email', 'spec@example.test');
  git('config', 'user.name', 'spec');
  fs.writeFileSync(path.join(directory, 'notes.md'), '# notes\n');
  git('add', '.');
  git('commit', '-m', 'init');
  fs.appendFileSync(path.join(directory, 'notes.md'), '\nmore\n');
  return directory;
}
