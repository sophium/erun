import * as React from 'react';

import type { TerminalController } from './TerminalController';

export const ControllerContext = React.createContext<TerminalController | null>(null);

// The controller is only for imperative side effects; state goes through dispatch.
export function useController(): TerminalController {
  const controller = React.useContext(ControllerContext);
  if (!controller) {
    throw new Error('useController must be called inside <ControllerProvider>');
  }
  return controller;
}
