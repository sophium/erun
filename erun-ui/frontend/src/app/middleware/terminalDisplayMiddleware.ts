import { createListenerMiddleware } from '@reduxjs/toolkit';

import { setSessionId } from '../slices/terminalSlice';
import type { AppDispatch, RootState } from '../store';
import { thunkExtra } from '../thunkExtra';

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
    // The controller coalesces switches and hands off only after the outgoing
    // session's output has parsed.
    controller.requestSessionSwitch(sessionId);
  },
});
