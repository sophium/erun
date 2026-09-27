import {
  clearSelectedSessionForEnv,
  clearTabsForEnv,
  setSelectedSessionForEnv,
  setTabsForEnv,
} from './slices/terminalSlice';
import type { TerminalTab, TerminalTabKind } from './state';
import type { AppThunk, RootState } from './store';
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

// Keeps the tab list sorted so the strip layout stays stable across re-renders.
export const recordTab =
  (key: string, sessionId: number, slot: number, kind: TerminalTabKind, label: string): AppThunk =>
  (dispatch, getState) => {
    const current = getState().terminal.tabsByEnv[key];
    const tabs = current ? [...current] : [];
    const existingIndex = tabs.findIndex((tab) => tab.kind === kind && tab.slot === slot);
    if (existingIndex >= 0) {
      tabs[existingIndex] = { sessionId, slot, kind, label };
    } else {
      tabs.push({ sessionId, slot, kind, label });
      tabs.sort(compareTabs);
    }
    dispatch(setTabsForEnv({ key, tabs }));
  };

// envKeyForSession names the env whose tab strip still lists a session. Only
// the env's ERun tab carries a tracked openSelection; its Local and AI tabs
// register none, so a caller that needs the env of an arbitrary session reads
// it here rather than from the sessions slice.
export function envKeyForSession(state: RootState, sessionId: number): string | undefined {
  for (const [key, tabs] of Object.entries(state.terminal.tabsByEnv)) {
    if (tabs.some((tab) => tab.sessionId === sessionId)) {
      return key;
    }
  }
  return undefined;
}

// envSessionIds lists the sessions an env's tab strip currently holds: the
// desktop's sessions for that env, as the frontend knows them. A close that
// wants to know what it is about to tear down reads them here rather than
// guessing, and the desktop's own close snapshots the same set — every session
// bound to the (tenant, env) pair — under its lock.
export function envSessionIds(state: RootState, key: string): number[] {
  return (state.terminal.tabsByEnv[key] ?? []).map((tab) => tab.sessionId);
}

// Returns the remaining tabs so the caller can pick a new active session.
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
    return remaining;
  };

// Remembers the last-viewed tab so re-opening the env restores it.
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
