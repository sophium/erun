import type { Page } from '@playwright/test';

import { expect, test, withTestBudget } from '../../../fixtures/erunApp.js';
import {
  removeOrchestrator,
  SEED_ENV_ALPHA,
  SEED_ORCHESTRATOR,
  SEED_TENANT,
  setOpenRouterCatalog,
} from '../../../fixtures/seedRoot.js';

// What a launch does with these settings is not observable here; Go tests own
// that. Each case uses a throwaway orchestrator and restores any staged catalog.
const DEFAULT_MODEL_LABEL = 'Default (opus)';
const DEFAULT_EFFORT_LABEL = 'Default (ultracode)';

// The picker must omit models erun cannot drive: choosing one is refused at save.
const UNDRIVEABLE_MODEL = 'deepseek/deepseek-v4.1-flash';
const CATALOG = {
  baseUrl: 'https://gateway.example.test/api',
  models: ['acme/coder-large', 'acme/coder-small', UNDRIVEABLE_MODEL],
  defaultModel: 'acme/coder-small',
};
const DRIVEABLE_MODELS = ['acme/coder-large', 'acme/coder-small'];

test.describe('the orchestrator dialog sets how its session launches', () => {
  test('settings survive create, reopen and an unrelated rename, and clear to defaults', async ({
    app,
  }) => {
    const name = 'launch-settings-test';
    const renamed = 'launch-settings-test-renamed';

    try {
      await app.sidebar.newOrchestratorButton().click();
      await app.orchestratorDialog.waitForOpen();
      await app.orchestratorDialog.toggleEnv(SEED_TENANT, SEED_ENV_ALPHA);

      await expect(app.orchestratorDialog.gatewayCheckbox()).not.toBeChecked();
      await expect(app.orchestratorDialog.modelInput()).toHaveValue('');
      await expect(app.orchestratorDialog.modelInput()).toHaveAttribute(
        'placeholder',
        DEFAULT_MODEL_LABEL,
      );
      await expect(app.orchestratorDialog.effortTrigger()).toHaveText(DEFAULT_EFFORT_LABEL);

      await app.orchestratorDialog.modelInput().fill('sonnet');
      await app.orchestratorDialog.setEffort('high');
      await app.orchestratorDialog.create(name);
      await app.orchestratorDialog.waitForClosed();

      // Reopening reloads from the persisted config.
      await app.sidebar.openOrchestratorDialog(name);
      await expect(app.orchestratorDialog.modelInput('Edit orchestrator')).toHaveValue('sonnet');
      await expect(app.orchestratorDialog.effortTrigger('Edit orchestrator')).toHaveText('high');

      // The save replaces the whole entry, so unsent settings would be erased.
      await app.orchestratorDialog.nameInput('Edit orchestrator').fill(renamed);
      await app.orchestratorDialog.save();
      await app.orchestratorDialog.waitForClosed('Edit orchestrator');

      await app.sidebar.openOrchestratorDialog(renamed);
      await expect(app.orchestratorDialog.nameInput('Edit orchestrator')).toHaveValue(renamed);
      await expect(app.orchestratorDialog.modelInput('Edit orchestrator')).toHaveValue('sonnet');
      await expect(app.orchestratorDialog.effortTrigger('Edit orchestrator')).toHaveText('high');

      await app.orchestratorDialog.modelResetButton('Edit orchestrator').click();
      await app.orchestratorDialog.setEffort(DEFAULT_EFFORT_LABEL, 'Edit orchestrator');
      await app.orchestratorDialog.save();
      await app.orchestratorDialog.waitForClosed('Edit orchestrator');

      await app.sidebar.openOrchestratorDialog(renamed);
      await expect(app.orchestratorDialog.modelInput('Edit orchestrator')).toHaveValue('');
      await expect(app.orchestratorDialog.effortTrigger('Edit orchestrator')).toHaveText(
        DEFAULT_EFFORT_LABEL,
      );
      await app.orchestratorDialog.cancel('Edit orchestrator');
    } finally {
      removeOrchestrator(name);
      removeOrchestrator(renamed);
    }
  });

  test('a model the launch guard refuses is flagged and its save refused', async ({ app }) => {
    const name = 'launch-settings-invalid-model';

    try {
      await app.sidebar.newOrchestratorButton().click();
      await app.orchestratorDialog.waitForOpen();
      await app.orchestratorDialog.toggleEnv(SEED_TENANT, SEED_ENV_ALPHA);

      await app.orchestratorDialog.modelInput().fill('--dangerously-skip-permissions');
      await expect(app.orchestratorDialog.modelInput()).toHaveAttribute('aria-invalid', 'true');
      await expect(app.orchestratorDialog.modelHelp()).toContainText('cannot start with -');

      await app.orchestratorDialog.create(name);
      await expect(app.orchestratorDialog.errorAlert()).toContainText('invalid model');
      await expect(app.orchestratorDialog.modelInput()).toBeVisible();
      await app.orchestratorDialog.cancel();
    } finally {
      removeOrchestrator(name);
    }
  });

  test('typing the clearing word as the model saves the default', async ({ app }) => {
    const name = 'launch-settings-typed-default';

    try {
      await app.sidebar.newOrchestratorButton().click();
      await app.orchestratorDialog.waitForOpen();
      await app.orchestratorDialog.toggleEnv(SEED_TENANT, SEED_ENV_ALPHA);

      await app.orchestratorDialog.modelInput().fill('Default');
      await expect(app.orchestratorDialog.modelInput()).not.toHaveAttribute('aria-invalid', 'true');
      await expect(app.orchestratorDialog.modelHelp()).toHaveText(
        'Using the default (opus). Type a model id to launch on another.',
      );
      await app.orchestratorDialog.setEffort('high');
      await app.orchestratorDialog.create(name);
      await app.orchestratorDialog.waitForClosed();

      await app.sidebar.openOrchestratorDialog(name);
      await expect(app.orchestratorDialog.effortTrigger('Edit orchestrator')).toHaveText('high');
      await expect(app.orchestratorDialog.modelInput('Edit orchestrator')).toHaveValue('');
      await expect(app.orchestratorDialog.modelInput('Edit orchestrator')).toHaveAttribute(
        'placeholder',
        DEFAULT_MODEL_LABEL,
      );
      await app.orchestratorDialog.cancel('Edit orchestrator');
    } finally {
      removeOrchestrator(name);
    }
  });

  test('the gateway is offered only once an erun-level catalog exists', async ({ app }) => {
    const name = 'launch-settings-gateway';

    // The baseline configures no catalog, so the gateway switch is disabled.
    await app.sidebar.newOrchestratorButton().click();
    await app.orchestratorDialog.waitForOpen();
    await expect(app.orchestratorDialog.gatewayCheckbox()).toBeDisabled();
    await expect(app.orchestratorDialog.gatewayHelp()).toContainText(
      'No gateway catalog is configured',
    );
    await expect(app.orchestratorDialog.gatewayHelp()).toContainText('ERun settings');
    await app.orchestratorDialog.cancel();
    await app.orchestratorDialog.waitForClosed();

    const restore = setOpenRouterCatalog(CATALOG);
    try {
      await app.sidebar.newOrchestratorButton().click();
      await app.orchestratorDialog.waitForOpen();
      await app.orchestratorDialog.toggleEnv(SEED_TENANT, SEED_ENV_ALPHA);
      await expect(app.orchestratorDialog.gatewayCheckbox()).toBeEnabled();

      await app.orchestratorDialog.gatewayCheckbox().check();

      await expect(app.orchestratorDialog.modelTrigger()).toHaveText(
        `Default (${CATALOG.defaultModel})`,
      );
      expect(await app.orchestratorDialog.gatewayModelOptionNames()).toEqual([
        `Default (${CATALOG.defaultModel})`,
        ...DRIVEABLE_MODELS,
      ]);

      const chosen = DRIVEABLE_MODELS[0] ?? '';
      await app.orchestratorDialog.setGatewayModel(chosen);
      await app.orchestratorDialog.create(name);
      await app.orchestratorDialog.waitForClosed();

      await app.sidebar.openOrchestratorDialog(name);
      await expect(app.orchestratorDialog.gatewayCheckbox('Edit orchestrator')).toBeChecked();
      await expect(app.orchestratorDialog.modelTrigger('Edit orchestrator')).toHaveText(chosen);

      await app.orchestratorDialog.gatewayCheckbox('Edit orchestrator').uncheck();
      await expect(app.orchestratorDialog.modelInput('Edit orchestrator')).toHaveValue(chosen);
      await app.orchestratorDialog.cancel('Edit orchestrator');
    } finally {
      removeOrchestrator(name);
      restore();
    }
  });
});

const RUNNING_SESSION_ID = 7474;

function runningSnapshot(overrides: Record<string, unknown> = {}) {
  return {
    id: SEED_ORCHESTRATOR,
    name: SEED_ORCHESTRATOR,
    environments: [{ tenant: SEED_TENANT, environment: SEED_ENV_ALPHA, directory: '/tmp/a' }],
    tenants: [SEED_TENANT],
    directories: ['/tmp/a'],
    sessionId: RUNNING_SESSION_ID,
    status: 'running',
    busy: false,
    transient: false,
    shellRunning: false,
    shellCommand: '',
    shellStartedAtUnix: 0,
    nudgeCount: 0,
    nudgeCapped: false,
    restartRequired: false,
    roleChanged: false,
    effort: 'high',
    launchSettingsChanged: true,
    ...overrides,
  };
}

async function stubOrchestratorList(page: Page, body: unknown): Promise<void> {
  await page.route('**/__erun_invoke', async (route, request) => {
    const parsed = JSON.parse(request.postData() ?? '{}') as { method?: string };
    if (parsed.method === 'ListOrchestrators') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: [body] }),
      });
    }
    await route.continue();
  });
}

test.describe('a running orchestrator whose launch settings changed under it', () => {
  test('the dialog asks for a restart, naming the launch settings as the cause', async ({
    app,
    page,
  }) => {
    await stubOrchestratorList(page, runningSnapshot());
    await app.reboot();

    // Not a scope change, so the sidebar keeps its plain running light.
    await expect(app.sidebar.orchestratorStatusDot(SEED_ORCHESTRATOR, 'running')).toBeVisible();

    await app.sidebar.openOrchestratorDialog(SEED_ORCHESTRATOR);
    await expect(app.orchestratorDialog.launchSettingsRestartNotice()).toBeVisible();
    await expect(app.orchestratorDialog.launchSettingsRestartNowButton()).toBeVisible();
    await expect(app.orchestratorDialog.restartRequiredNotice()).toHaveCount(0);
    await expect(app.orchestratorDialog.footerRestartButton()).toHaveText('Restart to apply');
    await expect(app.orchestratorDialog.effortTrigger('Edit orchestrator')).toHaveText('high');

    let restartedID = '';
    await page.route('**/__erun_invoke', async (route, request) => {
      const parsed = JSON.parse(request.postData() ?? '{}') as {
        method?: string;
        args?: [string?];
      };
      if (parsed.method === 'RestartOrchestrator') {
        restartedID = parsed.args?.[0] ?? '';
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({
            data: runningSnapshot({
              sessionId: RUNNING_SESSION_ID + 1,
              launchSettingsChanged: false,
            }),
          }),
        });
      }
      await route.continue();
    });

    await app.orchestratorDialog.launchSettingsRestartNowButton().click();
    await expect.poll(() => restartedID, withTestBudget()).toBe(SEED_ORCHESTRATOR);
    await app.orchestratorDialog.waitForClosed('Edit orchestrator');
  });

  test('a running orchestrator whose settings match shows no notice', async ({ app, page }) => {
    await stubOrchestratorList(page, runningSnapshot({ launchSettingsChanged: false }));
    await app.reboot();

    await app.sidebar.openOrchestratorDialog(SEED_ORCHESTRATOR);
    await expect(app.orchestratorDialog.effortTrigger('Edit orchestrator')).toHaveText('high');
    await expect(app.orchestratorDialog.launchSettingsRestartNotice()).toHaveCount(0);
    await expect(app.orchestratorDialog.footerRestartButton()).toHaveText('Restart');
    await app.orchestratorDialog.cancel('Edit orchestrator');
  });
});

const CATALOG_READ_ERROR =
  'cannot read the gateway catalog from the ERun settings: yaml: line 4: did not find expected key';

test.describe('the orchestrator dialog reports what the gateway catalog cannot offer', () => {
  test('a failed catalog read is reported once and can be retried', async ({ app, page }) => {
    await stubOrchestratorList(
      page,
      runningSnapshot({ useGateway: true, launchSettingsChanged: false }),
    );
    let failNextRead = true;
    let reads = 0;
    await page.route('**/__erun_invoke', async (route, request) => {
      const parsed = JSON.parse(request.postData() ?? '{}') as { method?: string };
      if (parsed.method === 'ListOrchestratorLaunchChoices') {
        reads += 1;
        if (failNextRead) {
          failNextRead = false;
          return route.fulfill({
            contentType: 'application/json',
            body: JSON.stringify({ error: CATALOG_READ_ERROR }),
          });
        }
      }
      await route.fallback();
    });
    await app.reboot();

    await app.sidebar.openOrchestratorDialog(SEED_ORCHESTRATOR);
    const mode = 'Edit orchestrator';
    await expect(app.orchestratorDialog.gatewayHelp(mode)).toHaveText(CATALOG_READ_ERROR);
    await expect(
      app.orchestratorDialog.textOccurrences('cannot read the gateway catalog', mode),
    ).toHaveCount(1);
    await expect(
      app.orchestratorDialog.textOccurrences('Correct the gateway settings', mode),
    ).toBeVisible();
    // Turning the gateway off needs no catalog.
    await expect(app.orchestratorDialog.gatewayCheckbox(mode)).toBeChecked();
    await expect(app.orchestratorDialog.gatewayCheckbox(mode)).toBeEnabled();

    // The baseline has no catalog, so the retry reports that instead of a read failure.
    await app.orchestratorDialog.gatewayRetryButton(mode).click();
    await expect(app.orchestratorDialog.gatewayHelp(mode)).toContainText(
      'No gateway catalog is configured, so this orchestrator cannot start through it',
    );
    await expect(app.orchestratorDialog.gatewayRetryButton(mode)).toHaveCount(0);
    expect(reads).toBe(2);

    await app.orchestratorDialog.gatewayCheckbox(mode).uncheck();
    await expect(app.orchestratorDialog.gatewayCheckbox(mode)).not.toBeChecked();
    await app.orchestratorDialog.cancel(mode);
  });

  test('a stored model the gateway cannot drive is flagged before a save', async ({
    app,
    page,
  }) => {
    const restore = setOpenRouterCatalog(CATALOG);
    try {
      await stubOrchestratorList(
        page,
        runningSnapshot({
          useGateway: true,
          model: UNDRIVEABLE_MODEL,
          launchSettingsChanged: false,
        }),
      );
      await app.reboot();

      await app.sidebar.openOrchestratorDialog(SEED_ORCHESTRATOR);
      const mode = 'Edit orchestrator';
      await expect(app.orchestratorDialog.gatewayCheckbox(mode)).toBeChecked();
      await expect(app.orchestratorDialog.modelTrigger(mode)).toHaveText(
        `${UNDRIVEABLE_MODEL} (cannot be driven through the gateway)`,
      );
      await expect(app.orchestratorDialog.modelTrigger(mode)).toHaveAttribute(
        'aria-invalid',
        'true',
      );
      await expect(app.orchestratorDialog.modelHelp(mode)).toContainText(
        `The gateway cannot drive ${UNDRIVEABLE_MODEL}`,
      );

      await app.orchestratorDialog.setGatewayModel(DRIVEABLE_MODELS[0] ?? '', mode);
      await expect(app.orchestratorDialog.modelTrigger(mode)).toHaveAttribute(
        'aria-invalid',
        'false',
      );
      await app.orchestratorDialog.cancel(mode);
    } finally {
      restore();
    }
  });
});
