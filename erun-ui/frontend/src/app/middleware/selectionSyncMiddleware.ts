import { createListenerMiddleware } from '@reduxjs/toolkit';

import { refreshIdleStatus } from '../idleThunks';
import { setSelected } from '../slices/selectionSlice';
import { setSessionId } from '../slices/terminalSlice';
import type { AppDispatch, RootState } from '../store';
import { thunkExtra } from '../thunkExtra';
import { selectionKey } from '../versionSuggestions';

// selectionSyncMiddleware reconciles state.terminal.sessionId with the
// currently selected env every time state.selection.selected changes.
//
// Without this, paths that dispatch setSelected without going through
// openSelection (boot persistence rehydrate, startInitSelection,
// startDeploySelection, manageHiddenSessionThunks, error rollbacks, the
// "click-the-already-selected-env" no-op in prepareOpenSelection) leave
// the terminal pointing at whatever session was previously visible —
// which can be a completely different env's PTY. The sidebar then shows
// env A as selected while the terminal renders env B's prompt and
// content, and input typed by the user is sent to the wrong PTY.
//
// The reconcile follows the same rule the explicit surfaceEnvSession
// helper in sessionThunks uses on the spawn path: if the current session
// already belongs to the new env's tab list, leave it alone; otherwise
// pick the remembered tab if it still exists, else any Local tab, else 0
// (the display middleware translates that into resetTerminal()).
export const selectionSyncMiddleware = createListenerMiddleware();

const startListening = selectionSyncMiddleware.startListening.withTypes<RootState, AppDispatch>();

startListening({
  actionCreator: setSelected,
  effect: (_action, listenerApi) => {
    const state = listenerApi.getState();
    const next = reconcileSessionForSelection(state);
    if (next !== state.terminal.sessionId) {
      listenerApi.dispatch(setSessionId(next));
    }
    // Fire an immediate idle-status refresh so the titlebar Play button
    // (and any other UI gated on idleStatus) reflects the new env within
    // ~50 ms instead of waiting up to the next 1 s polling tick. Without
    // this, clicking a stopped remote env left the user with no visible
    // Start affordance for almost a full second.
    if (thunkExtra.controller) {
      void listenerApi.dispatch(refreshIdleStatus());
    }
  },
});

function reconcileSessionForSelection(state: RootState): number {
  const selected = state.selection.selected;
  if (!selected) {
    return 0;
  }
  const key = selectionKey(selected);
  const tabs = state.terminal.tabsByEnv[key] ?? [];
  const currentSessionId = state.terminal.sessionId;
  if (currentSessionId > 0 && tabs.some((tab) => tab.sessionId === currentSessionId)) {
    return currentSessionId;
  }
  const remembered = state.terminal.selectedSessionByEnv[key] ?? 0;
  if (tabs.some((tab) => tab.sessionId === remembered)) {
    return remembered;
  }
  return tabs.find((tab) => tab.kind === 'local')?.sessionId ?? 0;
}
