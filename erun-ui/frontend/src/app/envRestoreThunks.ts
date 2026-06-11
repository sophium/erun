import type { UISelection } from '@/types';

import { ensureDefaultEnvTabs } from './sessionThunks';
import type { AppThunk } from './store';
import { requireController } from './thunkExtra';
import { selectionKey } from './versionSuggestions';

// envRestoreThunks own the recovery path where an env's linked cloud
// context returns to Running after the user's AI/ERun tabs were
// dropped while the context was stopped. The tabs are dropped by
// handleTerminalExit because tryReconnect refuses respawn against a
// stopped context (see erun-ui/terminal_sessions.go:973). Without an
// explicit restore on the context-running transition, the user clicks
// Play, the env reaches Running, but the tabs stay gone for the rest
// of the env's life. The titlebar Play button's success path already
// dispatches openSelection (which re-runs ensureDefaultEnvTabs); this
// file covers the recovery-after-transient-error path where the start
// command errored but the instance landed in Running anyway through
// another route — reached from idleThunks.refreshIdleStatus when the
// poll detects the cloud-context status transition.

// restoreEnvTabsAfterContextRunning re-creates any missing ERun/AI/Local
// tabs for the currently-selected env. No-op when no env is selected,
// when the selection has drifted, or when all default tabs already
// exist (the user is back to a healthy state). Nielsen #1 (visibility
// of system status) + #4 (consistency): start succeeds → working tabs
// return automatically without the user clicking the env again.
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
    if (hasAllDefaultTabs(getState().terminal.tabsByEnv[key] ?? [])) {
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

function hasAllDefaultTabs(tabs: { kind: string }[]): boolean {
  return (
    tabs.some((tab) => tab.kind === 'erun') &&
    tabs.some((tab) => tab.kind === 'ai') &&
    tabs.some((tab) => tab.kind === 'local')
  );
}
