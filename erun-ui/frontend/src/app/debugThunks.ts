import { setSessionDebug } from './slices/sessionsSlice';
import {
  setDebugOutput,
  setPendingDebugHeader as setPendingDebugHeaderAction,
} from './slices/terminalSlice';
import type { AppThunk } from './store';
import { trimDebugOutput } from './terminalStatus';

// Debug-pane display thunks. The "pending header" is the `$ command-line\n`
// preview shown before a new session takes over the terminal. Once the
// session opens, applyPendingDebugHeader copies the staged header into the
// session's debug buffer so the user sees what command they ran.

export const setPendingDebugHeader = (header: string): AppThunk =>
  (dispatch, getState) => {
    dispatch(setPendingDebugHeaderAction(header));
    if (getState().layout.debugOpen) {
      dispatch(setDebugOutput(header));
    }
  };

export const applyPendingDebugHeader = (sessionId: number): AppThunk =>
  (dispatch, getState) => {
    const pending = getState().terminal.pendingDebugHeader;
    if (!pending || sessionId <= 0) {
      dispatch(setPendingDebugHeaderAction(''));
      return;
    }
    if (getState().layout.debugOpen) {
      dispatch(setSessionDebug({ sessionId, value: pending }));
    }
    dispatch(setPendingDebugHeaderAction(''));
  };

export const syncDebugDisplay = (): AppThunk =>
  (dispatch, getState) => {
    const state = getState();
    if (!state.layout.debugOpen) {
      return;
    }
    const sessionId = state.terminal.sessionId;
    dispatch(setDebugOutput(state.sessions.debugBuffers[sessionId] || ''));
  };

// appendDebugOutput trims and appends to the per-session debug buffer.
// fromSessionId defaults to the active session — the source is explicit so
// background sessions (e.g. spawning ERun) can also log without flipping
// the visible buffer.
export const appendDebugOutput = (text: string, fromSessionId?: number): AppThunk =>
  (dispatch, getState) => {
    const state = getState();
    if (!state.layout.debugOpen || !text) {
      return;
    }
    const target = fromSessionId !== undefined ? fromSessionId : state.terminal.sessionId;
    const previous = state.sessions.debugBuffers[target] || '';
    const next = trimDebugOutput(previous + text);
    dispatch(setSessionDebug({ sessionId: target, value: next }));
    if (target === state.terminal.sessionId) {
      dispatch(setDebugOutput(next));
    }
  };
