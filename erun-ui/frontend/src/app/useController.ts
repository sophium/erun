import * as React from 'react';

import type { TerminalController } from './TerminalController';

export const ControllerContext = React.createContext<TerminalController | null>(null);

// useController exposes the imperative TerminalController instance to components
// that need to call its xterm/DOM/PTY-side helpers. State changes go through
// dispatch / thunks; the controller is reserved for imperative side effects
// the React layer cannot perform on its own (focus the terminal, queue a
// resize, read the live xterm cursor for a paste, etc.).
export function useController(): TerminalController {
  const controller = React.useContext(ControllerContext);
  if (!controller) {
    throw new Error('useController must be called inside <ControllerProvider>');
  }
  return controller;
}
