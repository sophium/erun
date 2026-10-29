import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';

import type { Page, Request, Route } from '@playwright/test';

import { test, expect } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT, removeOrchestrator } from '../../../fixtures/seedRoot.js';

// The native directory picker cannot be driven from a browser, so its invoke
// is stubbed to answer with the spec's directory.
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

function makeDirectory(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), 'erun-orchestrator-dir-'));
}

test.describe('the orchestrator dialog can bind a directory of its own', () => {
  test('a directory alone is enough to create an orchestrator, and it persists', async ({
    app,
    page,
  }) => {
    const directory = makeDirectory();
    await stubDirectoryPicker(page, directory);
    const name = 'directories-only-test';

    try {
      await app.sidebar.newOrchestratorButton().click();
      await app.orchestratorDialog.waitForOpen();
      await app.orchestratorDialog.directoriesAddButton().click();
      await expect(app.orchestratorDialog.directoryRow(directory)).toBeVisible();
      await app.orchestratorDialog.create(name);
      await app.orchestratorDialog.waitForClosed();

      // Reopening reloads from the persisted config, not the form's memory.
      await app.sidebar.openOrchestratorDialog(name);
      await expect(
        app.orchestratorDialog.directoryRow(directory, 'Edit orchestrator'),
      ).toBeVisible();
      await app.orchestratorDialog.cancel('Edit orchestrator');
      await app.orchestratorDialog.waitForClosed('Edit orchestrator');
    } finally {
      removeOrchestrator(name);
      fs.rmSync(directory, { recursive: true, force: true });
    }
  });

  // An env is linked too: removing a directory-only orchestrator's only
  // directory is refused.
  test('removing a directory persists through an edit', async ({ app, page }) => {
    const directory = makeDirectory();
    await stubDirectoryPicker(page, directory);
    const name = 'directories-remove-test';

    try {
      await app.sidebar.newOrchestratorButton().click();
      await app.orchestratorDialog.waitForOpen();
      await app.orchestratorDialog.toggleEnv(SEED_TENANT, SEED_ENV_ALPHA);
      await app.orchestratorDialog.directoriesAddButton().click();
      await expect(app.orchestratorDialog.directoryRow(directory)).toBeVisible();
      await app.orchestratorDialog.create(name);
      await app.orchestratorDialog.waitForClosed();

      await app.sidebar.openOrchestratorDialog(name);
      await app.orchestratorDialog.directoryRemoveButton(directory, 'Edit orchestrator').click();
      await expect(app.orchestratorDialog.directoryRow(directory, 'Edit orchestrator')).toHaveCount(
        0,
      );
      await app.orchestratorDialog.save();
      await app.orchestratorDialog.waitForClosed('Edit orchestrator');

      await app.sidebar.openOrchestratorDialog(name);
      await expect(app.orchestratorDialog.directoryRow(directory, 'Edit orchestrator')).toHaveCount(
        0,
      );
      await app.orchestratorDialog.cancel('Edit orchestrator');
      await app.orchestratorDialog.waitForClosed('Edit orchestrator');
    } finally {
      removeOrchestrator(name);
      fs.rmSync(directory, { recursive: true, force: true });
    }
  });

  // UpdateOrchestrator replaces the whole definition, so the form must send the
  // directory it just added.
  test('adding a directory to an existing orchestrator saves', async ({ app, page }) => {
    const directory = makeDirectory();
    await stubDirectoryPicker(page, directory);
    const name = 'directories-add-edit-test';

    try {
      await app.sidebar.newOrchestratorButton().click();
      await app.orchestratorDialog.waitForOpen();
      await app.orchestratorDialog.toggleEnv(SEED_TENANT, SEED_ENV_ALPHA);
      await app.orchestratorDialog.create(name);
      await app.orchestratorDialog.waitForClosed();

      await app.sidebar.openOrchestratorDialog(name);
      await app.orchestratorDialog.directoriesAddButton('Edit orchestrator').click();
      await expect(
        app.orchestratorDialog.directoryRow(directory, 'Edit orchestrator'),
      ).toBeVisible();
      await app.orchestratorDialog.save();
      await app.orchestratorDialog.waitForClosed('Edit orchestrator');

      await app.sidebar.openOrchestratorDialog(name);
      await expect(
        app.orchestratorDialog.directoryRow(directory, 'Edit orchestrator'),
      ).toBeVisible();
      await app.orchestratorDialog.cancel('Edit orchestrator');
      await app.orchestratorDialog.waitForClosed('Edit orchestrator');
    } finally {
      removeOrchestrator(name);
      fs.rmSync(directory, { recursive: true, force: true });
    }
  });

  // A running orchestrator reads back from the live session rather than the
  // persisted definition, so the two could disagree.
  test('adding a directory to a running orchestrator saves and reads back', async ({
    app,
    page,
  }) => {
    test.setTimeout(60_000);
    const directory = makeDirectory();
    await stubDirectoryPicker(page, directory);
    const name = 'directories-running-test';

    try {
      await app.sidebar.newOrchestratorButton().click();
      await app.orchestratorDialog.waitForOpen();
      await app.orchestratorDialog.toggleEnv(SEED_TENANT, SEED_ENV_ALPHA);
      await app.orchestratorDialog.create(name);
      await app.orchestratorDialog.waitForClosed();

      await app.sidebar.openOrchestratorSession(name);
      await expect(app.sidebar.orchestratorStatusDot(name, 'running')).toBeVisible({
        timeout: 25_000,
      });

      await app.sidebar.openOrchestratorDialog(name);
      await app.orchestratorDialog.directoriesAddButton('Edit orchestrator').click();
      await expect(
        app.orchestratorDialog.directoryRow(directory, 'Edit orchestrator'),
      ).toBeVisible();
      await app.orchestratorDialog.save();
      await app.orchestratorDialog.waitForClosed('Edit orchestrator');

      // The Edit form is populated from this payload, so a missing directory here
      // looks like a save that never happened.
      await app.sidebar.openOrchestratorDialog(name);
      await expect(app.orchestratorDialog.directoryRow(directory, 'Edit orchestrator')).toBeVisible(
        { timeout: 25_000 },
      );
      await app.orchestratorDialog.cancel('Edit orchestrator');
      await app.orchestratorDialog.waitForClosed('Edit orchestrator');
    } finally {
      removeOrchestrator(name);
      fs.rmSync(directory, { recursive: true, force: true });
    }
  });
});
