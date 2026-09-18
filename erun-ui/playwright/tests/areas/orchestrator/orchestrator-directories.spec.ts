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

  // Adding a directory to an EXISTING orchestrator and saving is its own path:
  // UpdateOrchestrator replaces the whole definition, and the form has to send the
  // directory it just added rather than only the ones it read back.
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

  // The combination the reported failure was in: a RUNNING orchestrator, edited
  // to add a directory. The running path answers from the live session rather
  // than from the persisted definition, so this is the one shape where the two
  // could disagree and make a save look like it did nothing.
  test('adding a directory to a running orchestrator saves and reads back', async ({
    app,
    page,
  }) => {
    // The only case in this file that drives a live session: it starts an
    // orchestrator, then edits it twice and reads back through the path that
    // answers from the running session rather than the persisted definition.
    // That is several round trips more than the suite-wide 30s per-test default
    // is sized for, so the whole-test clock is raised rather than left as the
    // shortest budget in the sequence.
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

      // Running before the edit, which is the state the report came from.
      // Starting a session is a real state transition whose cost is the
      // backend's, not this assertion's, so it converges against a budget
      // sized for that rather than against expect's 10s default.
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

      // The Edit form is populated from this payload, so a directory missing here
      // is indistinguishable from a save that never happened. This read goes
      // through the running session rather than the persisted definition, which
      // is the slower of the two paths, so it gets a budget of its own.
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
