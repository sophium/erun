import * as React from 'react';

import type { ERunUIController } from './ERunUIController';
import type { AppState } from './state';

export function useControllerState(controller: ERunUIController): AppState {
  const [, setVersion] = React.useState(0);

  React.useEffect(() => controller.subscribe(() => {
    setVersion((version) => version + 1);
  }), [controller]);

  return controller.state;
}
