import {
  CloseSession,
  StartSession,
} from '../../wailsjs/go/main/App';
import { sessionApi } from './api/sessionApi';
import { readError } from './errors';
import {
  dismissNotification,
  dismissTerminalStatus,
  hideTerminalMessage,
  showNotification,
  showTerminalFailure,
  showTerminalMessage,
} from './notificationThunks';
import type { AppThunk } from './store';
import { rebuildTerminalDisplayBuffer } from './terminalBuffers';
import {
  debugOutputBlock,
  formatIDECommand,
  ideLabel,
  ideOpenFailure,
} from './terminalStatus';
import { requireController } from './thunkExtra';
import { selectionKey } from './versionSuggestions';
import type { IDEKind } from './model';
import type {
  StartSessionResult,
  UISelection,
} from '@/types';

// sessionThunks own the terminal-session interactions exposed to components:
// opening an env, swapping between tabs in the strip, closing extra tabs,
// opening an IDE for the selection. These thunks call into the controller
// for xterm/PTY work (fitAddon, resetTerminal, registerOpenSessionResult,
// recordTab) while owning the user-facing state writes.

export const openSelection = (selection: UISelection): AppThunk<Promise<void>> =>
  async (_dispatch, _getState, extra) => {
    const controller = requireController(extra);
    await controller.openSelection(selection);
  };

export const addTerminalTab = (): AppThunk<Promise<void>> =>
  async (dispatch, _getState, extra) => {
    const controller = requireController(extra);
    const selection = controller.state.selected;
    if (!selection) {
      return;
    }
    const runSelection = { ...selection, debug: controller.state.debugOpen || undefined };
    const key = selectionKey(runSelection);
    const tabs = controller.state.tabsByEnv[key] || [];
    const nextSlot = tabs.reduce((max, tab) => (tab.slot >= max ? tab.slot + 1 : max), 0);
    const previousSessionId = controller.state.sessionId;
    try {
      const size = controller.terminalSize();
      const result = (await StartSession(runSelection, nextSlot, size.cols, size.rows)) as StartSessionResult;
      controller.registerOpenSessionResult(key, result, runSelection, previousSessionId);
      controller.focusTerminalSoon();
      controller.queueTerminalResize();
    } catch (error: unknown) {
      dispatch(showTerminalMessage(readError(error)));
    }
  };

export const selectTerminalTab = (sessionId: number): AppThunk =>
  (dispatch, _getState, extra) => {
    const controller = requireController(extra);
    if (sessionId <= 0 || sessionId === controller.state.sessionId) {
      return;
    }
    controller.state.sessionId = sessionId;
    controller.rememberSelectedTabForCurrentEnv(sessionId);
    controller.syncDebugDisplay();
    rebuildTerminalDisplayBuffer(controller.sessions, sessionId);
    controller.resetTerminal();
    controller.writeTerminalBuffer(controller.sessions.displayBuffer(sessionId));
    const exitReason = controller.sessions.exitReason(sessionId);
    if (exitReason) {
      controller.state.terminalCopyOutput = controller.sessions.exitOutput(sessionId);
      controller.state.terminalCopyStatus = '';
      dispatch(showTerminalMessage(exitReason));
    } else {
      dispatch(hideTerminalMessage());
    }
    controller.focusTerminalSoon();
    controller.queueTerminalResize();
  };

export const closeTerminalTab = (sessionId: number): AppThunk<Promise<void>> =>
  async (dispatch, _getState, extra) => {
    const controller = requireController(extra);
    if (sessionId <= 0) {
      return;
    }
    const selection = controller.state.selected;
    if (!selection) {
      return;
    }
    const runSelection = { ...selection, debug: controller.state.debugOpen || undefined };
    const key = selectionKey(runSelection);
    const tabs = controller.state.tabsByEnv[key] || [];
    const target = tabs.find((tab) => tab.sessionId === sessionId);
    if (target && target.kind !== 'extra') {
      return;
    }
    try {
      await CloseSession(sessionId);
    } catch (error: unknown) {
      dispatch(showTerminalMessage(readError(error)));
      return;
    }
    const remaining = controller.removeTab(key, sessionId);
    controller.sessions.clearSessionDebug(sessionId);
    if (controller.state.sessionId === sessionId) {
      const next = remaining[remaining.length - 1];
      if (next) {
        dispatch(selectTerminalTab(next.sessionId));
      } else {
        controller.state.sessionId = 0;
        controller.state.debugOutput = '';
        controller.resetTerminal();
      }
    }
  };

export const openIDE = (selection: UISelection | null, ide: IDEKind): AppThunk<Promise<void>> =>
  async (dispatch, _getState, extra) => {
    const controller = requireController(extra);
    if (!selection) {
      dispatch(showTerminalMessage('Choose an environment from the left pane.'));
      return;
    }
    const runSelection = { ...selection, debug: controller.state.debugOpen || undefined };
    const label = ideLabel(ide);
    controller.state.selected = selection;
    if (controller.state.debugOpen) {
      const header = `$ ${formatIDECommand(runSelection, ide)}\n`;
      controller.sessions.setSessionDebug(controller.state.sessionId, header);
      controller.syncDebugDisplay();
    }
    controller.state.terminalCopyOutput = '';
    controller.state.terminalCopyStatus = '';
    dispatch(showTerminalMessage(`Opening ${label} for ${selection.tenant} / ${selection.environment}...`));

    try {
      await dispatch(sessionApi.endpoints.openIDE.initiate({ selection: runSelection, ide })).unwrap();
    } catch (error: unknown) {
      const failure = ideOpenFailure(selection, label, readError(error));
      controller.appendDebugOutput(debugOutputBlock(failure.copyOutput));
      dispatch(dismissNotification());
      dispatch(showTerminalFailure(failure.message, failure.detail, failure.copyOutput, '', null));
      return;
    }
    dispatch(dismissTerminalStatus());
    dispatch(showNotification('success', `Opened ${label} for ${selection.tenant} / ${selection.environment}.`));
  };
