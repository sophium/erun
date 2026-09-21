import assert from 'node:assert/strict';

import { test } from 'vitest';

import type { UIDeployableComponent } from '@/types';

import {
  deployComponentsEmptySelection,
  toggleDeployComponentName,
} from './deployComponentsSelection';
import type { AppState } from './state';

type ManageDialog = AppState['manageDialog'];

// The checklist promises that Deploy rolls out exactly the checked charts, and an
// empty selection is the one state where it could not keep that: the desktop
// threads no --components flag for it, so `erun deploy` reads it as an omitted
// flag, treats it as unspecified, and falls through the precedence tiers to the
// runtime chart alone. The guard blocks that deploy. These pin both halves — the
// state it blocks, and the states it must leave alone, which is what makes it
// safe to put in front of an operator's Deploy button.

function component(name: string, selected: boolean): UIDeployableComponent {
  return { name, runtime: false, source: 'published-chart', selected };
}

function checklist(overrides: Partial<ManageDialog> = {}): ManageDialog {
  return {
    version: '1.0.20',
    deployComponents: [component('pw-devops', true), component('pw-backend-api', false)],
    deployComponentSelection: ['pw-devops'],
    deployComponentsLoading: false,
    ...overrides,
  } as unknown as ManageDialog;
}

test('unchecking the last checked chart is the state that blocks a deploy', () => {
  const options = [component('pw-devops', true), component('pw-backend-api', false)];
  const selection = toggleDeployComponentName(options, ['pw-devops'], 'pw-devops', false);
  assert.deepEqual(selection, []);
  assert.equal(
    deployComponentsEmptySelection(checklist({ deployComponentSelection: selection })),
    true,
  );

  // One chart is enough to deploy, so the guard is the empty set and not the
  // unchecking.
  assert.equal(deployComponentsEmptySelection(checklist()), false);
});

test('a checklist that never loaded is emptiness of information, not a choice', () => {
  // Naming an empty selection here would disable Deploy for a decision the
  // operator never made, and would strand the health check's runtime-not-deployed
  // recovery, which deploys the runtime on purpose.
  for (const dialog of [
    checklist({
      deployComponents: [],
      deployComponentSelection: [],
      deployComponentsLoading: true,
    }),
    checklist({ deployComponents: [], deployComponentSelection: [] }),
    checklist({ deployComponentSelection: [], version: '' }),
  ]) {
    assert.equal(deployComponentsEmptySelection(dialog), false);
  }
});
