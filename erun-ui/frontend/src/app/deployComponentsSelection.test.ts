import assert from 'node:assert/strict';

import { test } from 'vitest';

import type { UIDeployableComponent } from '@/types';

import {
  deployComponentsEmptySelection,
  toggleDeployComponentName,
} from './deployComponentsSelection';
import type { AppState } from './state';

type ManageDialog = AppState['manageDialog'];

// An empty selection omits --components, which deploys the runtime chart alone.

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

  assert.equal(deployComponentsEmptySelection(checklist()), false);
});

test('a checklist that never loaded is emptiness of information, not a choice', () => {
  // Before the checklist loads, emptiness is not the operator's choice.
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
