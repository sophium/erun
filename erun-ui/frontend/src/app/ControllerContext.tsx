import * as React from 'react';

import type { TerminalController } from './TerminalController';
import { ControllerContext } from './useController';

export function ControllerProvider({
  controller,
  children,
}: {
  controller: TerminalController;
  children: React.ReactNode;
}): React.ReactElement {
  return <ControllerContext.Provider value={controller}>{children}</ControllerContext.Provider>;
}
