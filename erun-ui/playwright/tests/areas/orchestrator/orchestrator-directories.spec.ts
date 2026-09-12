import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';

import type { Page, Request, Route } from '@playwright/test';

import { test, expect } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT, removeOrchestrator } from '../../../fixtures/seedRoot.js';

// An orchestrator's own directories are paths it works in that belong to no
// environment: no tenant, no pod, no cluster, no runtime version or image, and no
// role. These lock in the dialog's write path for them, because this dialog is
// the only desktop surface that sets them.
//
// The native directory picker cannot be driven from a browser, so the invoke it
// makes is stubbed the way tenant-dashboard-*.spec.ts stubs a backend read:
// ChooseLocalRepoPath answers with the directory the spec made.
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
  // A directory is a complete definition. This is the case the field exists for:
  // an operator points the orchestrator at a path without registering an
  // environment anywhere, and Create has to be offered with nothing else linked.
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

      // Reopening reloads from the persisted config, so this is the real
      // CreateOrchestrator round trip rather than the form's own memory.
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

  // Removing a directory persists through UpdateOrchestrator. An environment is
  // linked as well here, so the edit still has a scope to save -- removing the
  // only directory of a directory-only orchestrator would correctly be refused.
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

      // Gone in the persisted definition too, not just in the form.
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
});
