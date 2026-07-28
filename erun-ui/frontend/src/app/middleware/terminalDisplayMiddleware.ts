import { createListenerMiddleware } from '@reduxjs/toolkit';

import { setSessionId } from '../slices/terminalSlice';
import type { AppDispatch, RootState } from '../store';
import { rebuildTerminalDisplayBuffer } from '../terminalBuffers';
import { thunkExtra } from '../thunkExtra';

// Makes xterm output a derivation of the Redux terminal slice, so no caller
// has to pair a setSessionId dispatch with a manual reset/write. Re-opening
// the same env leaves its existing terminal buffer intact.
export const terminalDisplayMiddleware = createListenerMiddleware();

const startListening = terminalDisplayMiddleware.startListening.withTypes<RootState, AppDispatch>();

startListening({
  actionCreator: setSessionId,
  effect: (action, listenerApi) => {
    const controller = thunkExtra.controller;
    if (!controller) {
      return;
    }
    const previousSessionId = listenerApi.getOriginalState().terminal.sessionId;
    const sessionId = action.payload;
    if (sessionId === previousSessionId) {
      return;
    }
    if (sessionId <= 0) {
      controller.resetTerminal();
      return;
    }
    rebuildTerminalDisplayBuffer(controller.sessions, sessionId);
    controller.resetTerminal();
    controller.writeTerminalBuffer(sessionId, controller.sessions.displayBuffer(sessionId));
    // Push the pane geometry to the newly-active PTY so a session spawned at a
    // default size (an orchestrator starts at 80x24) redraws at the real width
    // instead of rendering its UI clipped.
    controller.resizeActiveSession();
  },
});
