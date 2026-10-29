import {
  clearSelectedSessionForEnv,
  clearTabsForEnv,
  setSelectedSessionForEnv,
  setTabsForEnv,
} from './slices/terminalSlice';
import type { TerminalTab, TerminalTabKind } from './state';
import type { AppThunk } from './store';
import { selectionKey } from './versionSuggestions';

const TAB_KIND_ORDER: Record<TerminalTabKind, number> = {
  local: 0,
  erun: 1,
  ai: 2,
  'contribute-erun': 3,
  'contribute-ai': 4,
  extra: 5,
};

function compareTabs(a: TerminalTab, b: TerminalTab): number {
  return TAB_KIND_ORDER[a.kind] - TAB_KIND_ORDER[b.kind] || a.slot - b.slot;
}

// forgetSessionIfDone drops retained output only once the session has exited
// and no tab shows it; called from both sides since either can come last.
export const forgetSessionIfDone =
  (sessionId: number): AppThunk =>
  (_dispatch, getState, extra) => {
    const state = getState();
    if (state.sessions.exitReasons[sessionId] === undefined) {
      return;
    }
    const shown = Object.values(state.terminal.tabsByEnv).some((tabs) =>
      tabs.some((tab) => tab.sessionId === sessionId),
    );
    if (shown) {
      return;
    }
    extra.controller?.sessions.forgetSession(sessionId);
  };

export const releaseExitedSession =
  (sessionId: number): AppThunk =>
  (dispatch, _getState, extra) => {
    extra.controller?.sessions.forgetSessionBuffer(sessionId);
    dispatch(forgetSessionIfDone(sessionId));
  };

export const recordTab =
  (key: string, sessionId: number, slot: number, kind: TerminalTabKind, label: string): AppThunk =>
  (dispatch, getState) => {
    const current = getState().terminal.tabsByEnv[key];
    const tabs = current ? [...current] : [];
    const existingIndex = tabs.findIndex((tab) => tab.kind === kind && tab.slot === slot);
    const replaced = existingIndex >= 0 ? tabs[existingIndex]?.sessionId : undefined;
    if (existingIndex >= 0) {
      tabs[existingIndex] = { sessionId, slot, kind, label };
    } else {
      tabs.push({ sessionId, slot, kind, label });
      tabs.sort(compareTabs);
    }
    dispatch(setTabsForEnv({ key, tabs }));
    if (replaced !== undefined && replaced !== sessionId) {
      dispatch(forgetSessionIfDone(replaced));
    }
  };

export const removeTab =
  (key: string, sessionId: number): AppThunk<TerminalTab[]> =>
  (dispatch, getState) => {
    const state = getState();
    const tabs = state.terminal.tabsByEnv[key];
    if (!tabs || tabs.length === 0) {
      return [];
    }
    const remaining = tabs.filter((tab) => tab.sessionId !== sessionId);
    if (remaining.length === 0) {
      dispatch(clearTabsForEnv(key));
    } else {
      dispatch(setTabsForEnv({ key, tabs: remaining }));
    }
    if (state.terminal.selectedSessionByEnv[key] === sessionId) {
      dispatch(clearSelectedSessionForEnv(key));
    }
    dispatch(forgetSessionIfDone(sessionId));
    return remaining;
  };

export const rememberSelectedTab =
  (sessionId: number): AppThunk =>
  (dispatch, getState) => {
    const state = getState();
    const selection = state.selection.selected;
    if (!selection) {
      return;
    }
    const key = selectionKey(selection);
    dispatch(setSelectedSessionForEnv({ key, sessionId }));
  };
