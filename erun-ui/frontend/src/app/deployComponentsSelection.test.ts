import { describe, expect, it } from 'vitest';

import type { UIDeployableComponent } from '@/types';

import { deploySelectionIsEmpty } from './deployComponentsSelection';

function options(...selected: boolean[]): UIDeployableComponent[] {
  return selected.map((isSelected, index) => ({
    name: index === 0 ? 'pw-devops' : `pw-component-${String(index)}`,
    runtime: index === 0,
    source: 'published-chart',
    selected: isSelected,
    publishedChart: '',
  }));
}

// An empty set from this dialog is an explicit "roll out nothing", but the
// resolver reads any empty set as "unspecified" and falls back to the runtime
// chart alone (erun-common/deploy_components.go). Deploy is gated on this
// predicate so the picker's "exactly the checked charts" stays true.
describe('deploySelectionIsEmpty', () => {
  it('reports a loaded, non-empty checklist with every box cleared', () => {
    expect(deploySelectionIsEmpty(options(true, false, false), [], false)).toBe(true);
  });

  it('stays false while a chart is still checked', () => {
    expect(deploySelectionIsEmpty(options(true, false), ['pw-devops'], false)).toBe(false);
  });

  it('stays false while the checklist is loading', () => {
    // The selection is unknown mid-probe, not deliberately cleared: Deploy is
    // already disabled by deployComponentsLoading, and the checklist must not be
    // blocked on a guess.
    expect(deploySelectionIsEmpty(options(true), [], true)).toBe(false);
  });

  it('stays false when the env offers no components at all', () => {
    // Nothing to check is not the operator clearing the list; an empty checklist
    // leaves Deploy to the resolver's default.
    expect(deploySelectionIsEmpty([], [], false)).toBe(false);
  });
});
