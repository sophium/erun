import type { UISelection } from '@/types';

import { environmentTypeIsHost } from './environmentType';
import { selectEnvironmentType } from './selectors';
import { ensureDefaultEnvTabs } from './sessionThunks';
import type { AppThunk } from './store';
import { requireController } from './thunkExtra';
import { selectionKey } from './versionSuggestions';

// Restores an env's default tabs when its stopped cloud context reaches Running
// by a route other than the titlebar Play success path.

// Safe on every status poll tick: a no-op once the tabs are back.
export const restoreEnvTabsAfterContextRunning =
  (selection: UISelection): AppThunk<Promise<void>> =>
  async (dispatch, getState, extra) => {
    const controller = requireController(extra);
    const current = getState().selection.selected;
    if (!isSameSelection(current, selection)) {
      return;
    }
    const runSelection = { ...selection };
    const key = selectionKey(runSelection);
    const hostEnv = environmentTypeIsHost(
      selectEnvironmentType(getState(), runSelection.tenant, runSelection.environment),
    );
    if (hasAllDefaultTabs(getState().terminal.tabsByEnv[key] ?? [], hostEnv)) {
      return;
    }
    const { cols, rows } = controller.terminalSize();
    await dispatch(ensureDefaultEnvTabs(runSelection, key, cols, rows));
  };

function isSameSelection(current: UISelection | null, target: UISelection): boolean {
  if (!current) {
    return false;
  }
  return current.tenant === target.tenant && current.environment === target.environment;
}

// A host env only ever has a Local tab, or this guard would never hold.
function hasAllDefaultTabs(tabs: { kind: string }[], hostEnv: boolean): boolean {
  if (hostEnv) {
    return tabs.some((tab) => tab.kind === 'local');
  }
  return (
    tabs.some((tab) => tab.kind === 'erun') &&
    tabs.some((tab) => tab.kind === 'ai') &&
    tabs.some((tab) => tab.kind === 'local')
  );
}
