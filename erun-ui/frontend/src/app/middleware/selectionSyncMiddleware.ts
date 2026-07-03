import { createListenerMiddleware } from '@reduxjs/toolkit';

import { refreshIdleStatus } from '../idleThunks';
import { setSelected } from '../slices/selectionSlice';
import { setSessionId } from '../slices/terminalSlice';
import type { AppDispatch, RootState } from '../store';
import { thunkExtra } from '../thunkExtra';
import { selectionKey } from '../versionSuggestions';

// Re-attaches the terminal to the selected env's session. setSelected paths
// that bypass openSelection would otherwise leave the terminal on the
// previously-visible session — possibly another env's PTY — so the sidebar
// shows one env while the terminal renders and takes input for another.
// Keep the reconcile rule in sync with surfaceEnvSession in sessionThunks;
// returning 0 tells the display middleware to resetTerminal().
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
    // Refresh idle status now so the titlebar Play button reflects the new
    // env immediately, not after the next poll leaves the user without a
    // visible Start affordance.
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
