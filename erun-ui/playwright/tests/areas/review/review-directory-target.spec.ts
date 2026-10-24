import { execFileSync } from 'node:child_process';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';

import type { Page, Request, Route } from '@playwright/test';

import { test, expect } from '../../../fixtures/erunApp.js';
import { removeOrchestrator } from '../../../fixtures/seedRoot.js';
import type { AppShell } from '../../../pages/index.js';

// A directory is a review target: its path labels the section and its diff is
// read with host git, not over an MCP edge.
test.describe('the diff panel shows a directory the orchestrator works in', () => {
  test('a directory-only orchestrator shows its changed files, and no environment is needed', async ({
    app,
    page,
  }) => {
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

      // A single target renders no header, so this note is the witness that the
      // panel switched to the directory target. No timeout: defers to the test's budget.
      const localDirectoryNote = page.getByText('Local directory — no hosted review');
      await localDirectoryNote.waitFor({ state: 'visible' });

      await expect(page.getByText('No environment selected')).toHaveCount(0);

      // The panel loads diffs on open and nothing retries a fetch that lands before
      // the target switch, so the refresh is re-issued on each attempt.
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

  // LoadDiff is held 27s, past the old 25s step cap, then continues to the real
  // backend so the diff is still read with host git.
  test('a directory diff that lands past the step cap is waited out, not cut off', async ({
    app,
    page,
  }) => {
    test.setTimeout(90_000);
    const directory = makeChangedGitRepo();
    const name = 'directory-diff-hold-test';
    // Routes do not chain, so the picker stub and the hold share one handler.
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

      // No timeout: defers to the test's budget.
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

// The panel resolves targets from the active orchestrator session.
async function openOrchestratorReviewPanel(app: AppShell, name: string): Promise<void> {
  await app.sidebar.openOrchestratorSession(name);
  if (!(await app.reviewPanel.isOpen())) {
    await app.titlebar.toggleReviewPanel();
    await app.reviewPanel.waitForOpen();
  }
}

// The native picker cannot be driven from a browser.
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
