import type { Page } from '@playwright/test';

import { expect, test, withTestBudget } from '../../../fixtures/erunApp.js';
import {
  removeOrchestrator,
  SEED_ENV_ALPHA,
  SEED_ORCHESTRATOR,
  SEED_TENANT,
  setOpenRouterCatalog,
} from '../../../fixtures/seedRoot.js';

// OrchestratorConfig.UseGateway/Model/Effort ship with a reader (`erun list`),
// CLI writers (`erun orchestrator set-gateway/set-model/set-effort`) and these
// dialog controls. This locks the desktop half: the controls default to the
// orchestrator's fixed defaults, persist across a real
// CreateOrchestrator/UpdateOrchestrator round trip, survive an unrelated edit,
// and clear back to defaults; the gateway switch is offered only once an
// erun-level catalog exists; and a running session whose settings changed
// under it asks for a restart.
//
// What a launch actually does with the settings is not observable here: the
// inert claude stub records neither argv nor environment, and the gateway
// credential lives in the operator secret store this harness must not touch.
// The Go tests in erun-ui/orchestrator_launch_settings_test.go own that branch:
// TestAnOrchestratorWithNoSettingsLaunchesExactlyAsBefore (the default command
// and env, byte for byte), TestEveryEffortLevelReachesTheLaunchFlags,
// TestAConfiguredModelReachesTheLaunchAndItsSubagents,
// TestAGatewayLaunchDeliversItsCredentialOnlyThroughTheEnvironment,
// TestAGatewayLaunchWithoutACredentialRefusesToStart,
// TestAGatewayWithNoCatalogRefusesToStartAndKeepsTheRunningSession and
// TestChangingLaunchSettingsOnARunningOrchestratorAsksForARestart.
//
// Each case creates a throwaway orchestrator and removes it again, the same
// isolation orchestrator-platform-alias.spec.ts uses; a staged catalog is
// restored in the same finally block so nothing leaks into a later spec on
// this worker's root config.
const DEFAULT_MODEL_LABEL = 'Default (opus)';
const DEFAULT_EFFORT_LABEL = 'Default (ultracode)';

// The catalog lists a model erun knows its lanes cannot drive alongside two it
// can. The picker must leave that one out: choosing it would be refused at
// save, and its listing would otherwise read as an offer.
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

      // The defaults are shown as defaults, naming what they resolve to, never
      // as a value silently chosen for the operator.
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

      // Reopening reloads from the persisted config, which is what proves the
      // settings survived the real CreateOrchestrator round trip.
      await app.sidebar.openOrchestratorDialog(name);
      await expect(app.orchestratorDialog.modelInput('Edit orchestrator')).toHaveValue('sonnet');
      await expect(app.orchestratorDialog.effortTrigger('Edit orchestrator')).toHaveText('high');

      // The save replaces the whole entry, so settings the dialog did not send
      // back would be erased by an edit that touches only the name.
      await app.orchestratorDialog.nameInput('Edit orchestrator').fill(renamed);
      await app.orchestratorDialog.save();
      await app.orchestratorDialog.waitForClosed('Edit orchestrator');

      await app.sidebar.openOrchestratorDialog(renamed);
      await expect(app.orchestratorDialog.nameInput('Edit orchestrator')).toHaveValue(renamed);
      await expect(app.orchestratorDialog.modelInput('Edit orchestrator')).toHaveValue('sonnet');
      await expect(app.orchestratorDialog.effortTrigger('Edit orchestrator')).toHaveText('high');

      // Clearing both persists too: the writer is not one-way.
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

  // A model that would reach --model as a flag is flagged where it is typed,
  // and the save is refused with the backend's own reason rather than stored
  // and dropped at launch.
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
      // Still open, so the operator can correct the value they typed.
      await expect(app.orchestratorDialog.modelInput()).toBeVisible();
      await app.orchestratorDialog.cancel();
    } finally {
      removeOrchestrator(name);
    }
  });

  // `erun orchestrator set-model <id> --model default` clears the model, in any
  // case. Typed into the free-entry field the same word must clear it too,
  // never be stored as a literal model the launch would then refuse.
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

    // The baseline configures no catalog. Turning the gateway on would be
    // refused at save and at launch, so the switch is disabled and says where
    // the catalog is configured.
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

      // Through the gateway the model is a pick from the catalog, and the
      // default names the catalog's own default rather than opus.
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

      // Off the gateway the stored model stays visible, now as free entry.
      await app.orchestratorDialog.gatewayCheckbox('Edit orchestrator').uncheck();
      await expect(app.orchestratorDialog.modelInput('Edit orchestrator')).toHaveValue(chosen);
      await app.orchestratorDialog.cancel('Edit orchestrator');
    } finally {
      removeOrchestrator(name);
      restore();
    }
  });
});

// A running session fixes its gateway, model and effort when it launches. The
// backend's comparison of what it launched with against what is configured is
// covered by TestChangingLaunchSettingsOnARunningOrchestratorAsksForARestart;
// this covers the rendering and the control the operator gets to resolve it.
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

    // Not a scope change, so the sidebar keeps its plain running light: the
    // session still holds every tool it should.
    await expect(app.sidebar.orchestratorStatusDot(SEED_ORCHESTRATOR, 'running')).toBeVisible();

    await app.sidebar.openOrchestratorDialog(SEED_ORCHESTRATOR);
    await expect(app.orchestratorDialog.launchSettingsRestartNotice()).toBeVisible();
    await expect(app.orchestratorDialog.launchSettingsRestartNowButton()).toBeVisible();
    await expect(app.orchestratorDialog.restartRequiredNotice()).toHaveCount(0);
    await expect(app.orchestratorDialog.footerRestartButton()).toHaveText('Restart to apply');
    // The form shows the configured value, not what the session launched with.
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

// The dialog reads the catalog when it opens. A read that fails is reported
// once, in the backend's own words, with a way to recover in place; and a
// stored model the catalog names as undriveable reads as that before a save.
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
    // A stored "on" stays switchable off: turning it off needs no catalog.
    await expect(app.orchestratorDialog.gatewayCheckbox(mode)).toBeChecked();
    await expect(app.orchestratorDialog.gatewayCheckbox(mode)).toBeEnabled();

    // The real read answers the retry: the baseline has no catalog, so the
    // stored "on" now reads as the problem it is instead of a read failure.
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

      // Choosing a model the gateway can drive clears the flag.
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
