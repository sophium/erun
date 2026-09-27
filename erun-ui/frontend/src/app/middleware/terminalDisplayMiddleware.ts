import { createListenerMiddleware } from '@reduxjs/toolkit';

import { setSessionId } from '../slices/terminalSlice';
import type { AppDispatch, RootState } from '../store';
import { thunkExtra } from '../thunkExtra';

// Makes xterm output a derivation of the Redux terminal slice, so no caller
// has to pair a setSessionId dispatch with a manual reset/write. Re-opening
// the same env leaves its existing terminal buffer intact.
export const terminalDisplayMiddleware = createListenerMiddleware();

const startListening = terminalDisplayMiddleware.startListening.withTypes<RootState, AppDispatch>();

startListening({
  actionCreator: setSessionId,
  effect: (action) => {
    const controller = thunkExtra.controller;
    if (!controller) {
      return;
    }
    // The switch is a request, not a hand-off the middleware can perform here:
    // what it has to move from is the session on the pane, and two dispatches
    // arriving before that pane has moved have to coalesce rather than each
    // snapshot, reset and activate in turn. Both of those are the controller's
    // to decide -- see requestSessionSwitch.
    controller.requestSessionSwitch(action.payload);
  },
});
