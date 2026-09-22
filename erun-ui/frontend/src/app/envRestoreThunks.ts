import type { UISelection } from '@/types';

import { environmentTypeIsHost } from './environmentType';
import { selectEnvironmentType } from './selectors';
import { ensureDefaultEnvTabs } from './sessionThunks';
import type { AppThunk } from './store';
import { requireController } from './thunkExtra';
import { selectionKey } from './versionSuggestions';

// envRestoreThunks own the recovery path where a stopped cloud context
// returns to Running after its env's AI/ERun tabs were already dropped.
// Nothing re-creates those tabs on that transition, so without an
// explicit restore they stay gone for the rest of the env's life. The
// titlebar Play success path already covers the normal case; this
// handles the alternate route where start errored but the instance
// reached Running anyway.

// Safe to dispatch from the status poll on every tick: it no-ops once
// the selected env already has its default tabs back.
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

// hostEnv narrows what "all" means: a host environment has no pod, so only its
// Local tab is ever created for it, and requiring the ERun and AI tabs would
// make this guard never hold -- the restore would re-run on every status poll,
// which is exactly what it exists to avoid.
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
