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

// recordTab inserts (or replaces) a tab for an env. The list stays sorted by
// kind then slot so the strip layout matches user expectations across
// re-renders.
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

// removeTab drops a session from the strip and clears its remembered-slot
// pointer if needed. Returns the remaining tabs so callers can pick a new
// active session.
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

// rememberSelectedTab pins the active session for the currently-selected env
// so re-opening it later switches back to the tab the user last viewed.
export const rememberSelectedTab =
  (sessionId: number): AppThunk =>
  (dispatch, getState) => {
    const state = getState();
    const selection = state.selection.selected;
    if (!selection) {
      return;
    }
    const key = selectionKey({ ...selection, debug: state.layout.debugOpen || undefined });
    dispatch(setSelectedSessionForEnv({ key, sessionId }));
  };
