import type { TerminalController } from './TerminalController';

// The store is constructed before the TerminalController, so the controller
// late-binds itself here; the reference stays null until then.
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
