import { CloseSession } from '../../wailsjs/go/main/App';
import { readError } from './errors';
import { hideTerminalMessage, showTerminalError, showTerminalMessage } from './notificationThunks';
import { setSessionId } from './slices/terminalSlice';
import { setTerminalCopyOutput, setTerminalCopyStatus } from './slices/terminalStatusSlice';
import type { AppThunk } from './store';
import { maybeRespawnDeadDefaultTab } from './tabRespawnThunks';
import { rememberSelectedTab, removeTab } from './tabsThunks';
import { requireController } from './thunkExtra';
import { selectionKey } from './versionSuggestions';

// Picking and closing one tab of the selected environment. Split out of
// ./sessionThunks to keep that file under the max-lines cap, the same way
// ./closeEnvironmentThunks holds the env-scoped teardown; sessionThunks
// re-exports both so existing imports still resolve.
export const selectTerminalTab =
  (sessionId: number): AppThunk =>
  (dispatch, getState, extra) => {
    const controller = requireController(extra);
    if (sessionId <= 0) {
      return;
    }
    if (dispatch(maybeRespawnDeadDefaultTab(sessionId))) {
      return;
    }
    if (sessionId === getState().terminal.sessionId) {
      return;
    }
    dispatch(setSessionId(sessionId));
    dispatch(rememberSelectedTab(sessionId));
    const state = getState();
    const exitReason = state.sessions.exitReasons[sessionId] ?? '';
    if (exitReason) {
      dispatch(setTerminalCopyOutput(state.sessions.exitOutputs[sessionId] ?? ''));
      dispatch(setTerminalCopyStatus(''));
      dispatch(showTerminalMessage(exitReason));
    } else {
      dispatch(hideTerminalMessage());
    }
    controller.focusTerminalSoon();
    controller.queueTerminalResize();
  };

export const closeTerminalTab =
  (sessionId: number): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    if (sessionId <= 0) {
      return;
    }
    const state = getState();
    const selection = state.selection.selected;
    if (!selection) {
      return;
    }
    const runSelection = { ...selection };
    const key = selectionKey(runSelection);
    const tabs = state.terminal.tabsByEnv[key] ?? [];
    const target = tabs.find((tab) => tab.sessionId === sessionId);
    if (target && target.kind !== 'extra') {
      return;
    }
    try {
      await CloseSession(sessionId);
    } catch (error: unknown) {
      dispatch(showTerminalError(readError(error)));
      return;
    }
    const remaining = dispatch(removeTab(key, sessionId));
    if (getState().terminal.sessionId === sessionId) {
      const next = remaining[remaining.length - 1];
      if (next) {
        dispatch(selectTerminalTab(next.sessionId));
      } else {
        dispatch(setSessionId(0));
      }
    }
  };
