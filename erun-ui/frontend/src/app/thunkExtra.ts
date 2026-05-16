import type { ERunUIController } from './ERunUIController';

// thunkExtra holds the late-bound ERunUIController reference that thunks
// reach for imperative side effects (xterm callbacks, session lifecycle,
// notifications). The store is constructed before the controller, so the
// controller writes itself into this holder in its constructor; thunks
// guard against null by asserting it before use.
export const thunkExtra: { controller: ERunUIController | null } = { controller: null };

export type ThunkExtra = typeof thunkExtra;

export function requireController(extra: ThunkExtra): ERunUIController {
  if (!extra.controller) {
    throw new Error('Thunk extra.controller is not set; ERunUIController must be constructed before dispatching workflow thunks.');
  }
  return extra.controller;
}
