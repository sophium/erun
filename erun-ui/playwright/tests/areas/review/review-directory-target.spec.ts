import { execFileSync } from 'node:child_process';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';

import type { Page, Request, Route } from '@playwright/test';

import { test, expect } from '../../../fixtures/erunApp.js';
import { removeOrchestrator } from '../../../fixtures/seedRoot.js';

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

      // The panel resolves its targets from the ACTIVE orchestrator session, so
      // the session has to be open before the panel has anything to show.
      await app.sidebar.openOrchestratorSession(name);
      if (!(await app.reviewPanel.isOpen())) {
        await app.titlebar.toggleReviewPanel();
      }
      // The panel loads its diffs when it opens, and it was already open when the
      // session became active, so this spec asks for the fetch explicitly rather
      // than waiting out the periodic refresh. A slot that has never been fetched
      // renders exactly like one whose diff is empty -- hence the explicit ask.
      await app.reviewPanel.refreshDiff();

      // The operator's symptom, pinned: this scope is reviewable, not empty.
      await expect(page.getByText('No environment selected')).toHaveCount(0);

      // The section says what it is. A single target renders no header (the panel
      // keeps the env-tab case chrome-free), so the note is what identifies this
      // as a directory section rather than an unlabelled environment one.
      await expect(page.getByText('Local directory — no hosted review')).toBeVisible();

      // And the changed file arrived, which is the directory's own diff having
      // been read with host git -- the whole point of the target existing.
      await expect.poll(() => app.reviewPanel.treeFilePaths()).toContain('notes.md');
    } finally {
      removeOrchestrator(name);
      fs.rmSync(directory, { recursive: true, force: true });
    }
  });
});

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
