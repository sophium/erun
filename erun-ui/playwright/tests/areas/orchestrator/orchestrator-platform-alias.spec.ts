import { test, expect } from '../../../fixtures/erunApp.js';
import {
  addERunCloudProviderAlias,
  SEED_ENV_ALPHA,
  SEED_TENANT,
  removeOrchestrator,
} from '../../../fixtures/seedRoot.js';

// OrchestratorConfig.Alias ships with a reader (`erun list`), a CLI writer
// (`erun orchestrator set-alias`) and a desktop control -- the field the
// erun-common operator-settable registry exists to stop from repeating
// OrchestratorEnvConfig.Role's reader-without-a-writer history. This locks the
// desktop half: a Platform alias control that offers only the erun-type
// aliases this machine has, defaults to declaring none, and persists across a
// real CreateOrchestrator/UpdateOrchestrator round trip rather than only this
// render's component state.
//
// The baseline seeds no erun-type alias (its two are aws and cloudflare), which
// is the state the last test below relies on and every other test has to lift:
// a real alias is staged per test through addERunCloudProviderAlias, and
// restored in the finally block, so nothing leaks into a later spec sharing
// this worker's root config.
//
// Creates a throwaway orchestrator rather than reusing SEED_ORCHESTRATOR, the
// same isolation orchestrator-env-role.spec.ts uses, and removes it again --
// there is no dialog affordance to delete a created orchestrator.
const STAGED_ALIAS = 'erun+api.acme.test@erun';

// The value the picker carries for "declares none of its own". Radix's
// Select.Item rejects an empty-string value, so the control represents the
// field's '' as this token and translates it back on save; the label is what an
// operator reads.
const DECLARE_NONE_LABEL = "This machine's own alias";

test.describe('the orchestrator dialog can declare a platform alias', () => {
  test('offers only the erun aliases, and a chosen one survives create then edit', async ({
    app,
  }) => {
    const name = 'platform-alias-test';
    const restore = addERunCloudProviderAlias(STAGED_ALIAS, 'https://api.acme.test');

    try {
      await app.sidebar.newOrchestratorButton().click();
      await app.orchestratorDialog.waitForOpen();
      await app.orchestratorDialog.toggleEnv(SEED_TENANT, SEED_ENV_ALPHA);

      // Declaring none is the default, not a silently chosen alias.
      await expect(app.orchestratorDialog.aliasTrigger()).toContainText(DECLARE_NONE_LABEL);

      // The staged erun alias is offered; the baseline's aws and cloudflare
      // aliases are not, because the writer refuses anything but an erun-type
      // alias and offering one would promise a save that cannot land.
      expect(await app.orchestratorDialog.aliasOptionNames()).toEqual([
        DECLARE_NONE_LABEL,
        STAGED_ALIAS,
      ]);

      await app.orchestratorDialog.setAlias(STAGED_ALIAS);
      await app.orchestratorDialog.create(name);
      await app.orchestratorDialog.waitForClosed();

      // Reopening reloads from the persisted config: this is what proves the
      // alias survived the real CreateOrchestrator round trip, not just that
      // the select renders whatever it was last set to in memory.
      await app.sidebar.openOrchestratorDialog(name);
      await expect(app.orchestratorDialog.aliasTrigger('Edit orchestrator')).toContainText(
        STAGED_ALIAS,
      );

      // And it survives an edit that touches something else entirely. The save
      // replaces the orchestrator's whole entry, so an alias the dialog did not
      // carry back — because the read model never showed it, or because the
      // save omitted it — would be erased by the rename below rather than left
      // alone.
      const renamed = 'platform-alias-test-renamed';
      await app.orchestratorDialog.nameInput('Edit orchestrator').fill(renamed);
      await app.orchestratorDialog.save();
      await app.orchestratorDialog.waitForClosed('Edit orchestrator');

      await app.sidebar.openOrchestratorDialog(renamed);
      await expect(app.orchestratorDialog.nameInput('Edit orchestrator')).toHaveValue(renamed);
      await expect(app.orchestratorDialog.aliasTrigger('Edit orchestrator')).toContainText(
        STAGED_ALIAS,
      );

      // Clearing it back to none persists too -- the writer is not one-way.
      await app.orchestratorDialog.setAlias(DECLARE_NONE_LABEL, 'Edit orchestrator');
      await app.orchestratorDialog.save();
      await app.orchestratorDialog.waitForClosed('Edit orchestrator');

      await app.sidebar.openOrchestratorDialog(renamed);
      await expect(app.orchestratorDialog.aliasTrigger('Edit orchestrator')).toContainText(
        DECLARE_NONE_LABEL,
      );

      await app.orchestratorDialog.cancel('Edit orchestrator');
    } finally {
      removeOrchestrator(name);
      restore();
    }
  });

  // A stored alias this machine no longer resolves still has to render. No item
  // matching the trigger's value leaves it blank, and the operator can then
  // neither see nor change the value the next save is about to refuse --
  // orchestratorAliasOptions keeps it on the list for exactly this case.
  test('keeps a stored alias the machine no longer resolves on the list', async ({ app }) => {
    const name = 'stale-alias-test';
    const restore = addERunCloudProviderAlias(STAGED_ALIAS, 'https://api.acme.test');

    try {
      await app.sidebar.newOrchestratorButton().click();
      await app.orchestratorDialog.waitForOpen();
      await app.orchestratorDialog.toggleEnv(SEED_TENANT, SEED_ENV_ALPHA);
      await app.orchestratorDialog.setAlias(STAGED_ALIAS);
      await app.orchestratorDialog.create(name);
      await app.orchestratorDialog.waitForClosed();

      // The alias goes away underneath the stored value -- a hand-edited
      // config, or an alias removed from this machine since it was recorded.
      restore();

      await app.sidebar.openOrchestratorDialog(name);
      await expect(app.orchestratorDialog.aliasTrigger('Edit orchestrator')).toContainText(
        STAGED_ALIAS,
      );
      expect(await app.orchestratorDialog.aliasOptionNames('Edit orchestrator')).toEqual([
        DECLARE_NONE_LABEL,
        STAGED_ALIAS,
      ]);

      await app.orchestratorDialog.cancel('Edit orchestrator');
    } finally {
      removeOrchestrator(name);
      restore();
    }
  });

  // With no erun alias configured at all there is still exactly one honest
  // choice -- declaring none -- and the control says so rather than rendering
  // an empty picker the operator cannot act on.
  test('offers declaring none when this machine has no erun alias', async ({ app }) => {
    await app.sidebar.newOrchestratorButton().click();
    await app.orchestratorDialog.waitForOpen();

    await expect(app.orchestratorDialog.aliasTrigger()).toContainText(DECLARE_NONE_LABEL);
    expect(await app.orchestratorDialog.aliasOptionNames()).toEqual([DECLARE_NONE_LABEL]);

    await app.orchestratorDialog.cancel();
  });
});
