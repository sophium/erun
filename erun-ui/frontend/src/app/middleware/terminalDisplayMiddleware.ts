import { createListenerMiddleware } from '@reduxjs/toolkit';

import { setSessionId } from '../slices/terminalSlice';
import type { AppDispatch, RootState } from '../store';
import { rebuildTerminalDisplayBuffer } from '../terminalBuffers';
import { thunkExtra } from '../thunkExtra';

// terminalDisplayMiddleware makes the xterm output a true derivation of the
// Redux terminal slice. Every setSessionId dispatch flows through here and
// triggers the imperative xterm refresh, so no caller has to remember to
// pair the dispatch with a manual reset/write. Only refreshes when the id
// actually changed, matching the prior behaviour of re-opening the same env
// without wiping its terminal buffer.
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
  },
});
