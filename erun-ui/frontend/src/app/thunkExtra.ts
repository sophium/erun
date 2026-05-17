import type { TerminalController } from './TerminalController';

// thunkExtra holds the late-bound TerminalController reference that thunks
// reach for imperative side effects (xterm callbacks, session lifecycle,
// notifications). The store is constructed before the controller, so the
// controller writes itself into this holder in its constructor; thunks
// guard against null by asserting it before use.
export const thunkExtra: { controller: TerminalController | null } = { controller: null };

export type ThunkExtra = typeof thunkExtra;

export function requireController(extra: ThunkExtra): TerminalController {
  if (!extra.controller) {
    throw new Error(
      'Thunk extra.controller is not set; TerminalController must be constructed before dispatching workflow thunks.',
    );
  }
  return extra.controller;
}
