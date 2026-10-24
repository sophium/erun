import assert from 'node:assert/strict';

import { test } from 'vitest';

import {
  ORCHESTRATOR_ALIAS_NONE,
  orchestratorAliasHelper,
  orchestratorAliasOptions,
  orchestratorAliasValue,
} from './OrchestratorDialog.Alias.helpers';

function optionValues(configured: string[], alias = ''): string[] {
  return orchestratorAliasOptions(configured, alias).map((option) => option.value);
}

// Must agree with eruncommon.ValidateOrchestratorAlias.

test('offers the configured aliases plus the declare-none sentinel', () => {
  assert.deepEqual(optionValues(['erun+a.example@erun', 'erun+b.example@erun']), [
    ORCHESTRATOR_ALIAS_NONE,
    'erun+a.example@erun',
    'erun+b.example@erun',
  ]);
});

test('offers the sentinel alone when this machine has no erun alias', () => {
  assert.deepEqual(optionValues([]), [ORCHESTRATOR_ALIAS_NONE]);
});

test('keeps a stored alias the host no longer resolves on the list', () => {
  assert.deepEqual(optionValues(['erun+a.example@erun'], 'erun+gone.example@erun'), [
    ORCHESTRATOR_ALIAS_NONE,
    'erun+a.example@erun',
    'erun+gone.example@erun',
  ]);
});

test('does not duplicate a stored alias that is still configured', () => {
  assert.deepEqual(optionValues(['erun+a.example@erun'], 'erun+a.example@erun'), [
    ORCHESTRATOR_ALIAS_NONE,
    'erun+a.example@erun',
  ]);
});

test('round-trips the declare-none sentinel to the empty field value', () => {
  assert.equal(orchestratorAliasValue(ORCHESTRATOR_ALIAS_NONE), '');
  assert.equal(orchestratorAliasValue('erun+a.example@erun'), 'erun+a.example@erun');
});

test('helper text distinguishes declaring none from declaring an alias', () => {
  assert.match(orchestratorAliasHelper(''), /No alias of its own/);
  assert.match(orchestratorAliasHelper('erun+a.example@erun'), /erun\+a\.example@erun/);
});
