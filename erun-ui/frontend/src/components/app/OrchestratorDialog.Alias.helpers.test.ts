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

// The picker's list is the frontend half of the check
// eruncommon.ValidateOrchestratorAlias applies on the Go side, and the two must
// agree: the writer refuses an alias this host cannot resolve, so offering one
// would let the operator choose a value whose save is guaranteed to fail.

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

// A value can be on disk from a hand-edited config.yaml, or from an alias that
// has since been removed from this machine. Edit mode has to render it: no item
// matching the stored value leaves the trigger blank, and the operator can
// neither see nor change the value the next save is about to refuse.
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

// Radix's Select.Item rejects an empty-string value, so '' travels as the
// sentinel and is translated back at this one boundary.
test('round-trips the declare-none sentinel to the empty field value', () => {
  assert.equal(orchestratorAliasValue(ORCHESTRATOR_ALIAS_NONE), '');
  assert.equal(orchestratorAliasValue('erun+a.example@erun'), 'erun+a.example@erun');
});

test('helper text distinguishes declaring none from declaring an alias', () => {
  assert.match(orchestratorAliasHelper(''), /No alias of its own/);
  assert.match(orchestratorAliasHelper('erun+a.example@erun'), /erun\+a\.example@erun/);
});
