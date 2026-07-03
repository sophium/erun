import type { Middleware } from '@reduxjs/toolkit';

import { recordUITraceEntry } from '../uiTraceBuffer';

// uiTraceMiddleware records every dispatched action (thunks surface through
// the plain actions they dispatch, RTK-Query through its lifecycle actions)
// plus the top-level state slices the action changed, into the non-Redux
// ring buffer the Diagnostics console's "UI trace" tab renders. Shallow
// slice comparison keeps the recorder O(slices) per action — no deep
// diffing on the dispatch path.
export const uiTraceMiddleware: Middleware = (storeApi) => (next) => (action) => {
  const before = storeApi.getState() as Record<string, unknown>;
  const result = next(action);
  const after = storeApi.getState() as Record<string, unknown>;
  const changed: string[] = [];
  for (const key of Object.keys(after)) {
    if (before[key] !== after[key]) {
      changed.push(key);
    }
  }
  const type =
    typeof action === 'object' && action !== null && 'type' in action
      ? String(action.type)
      : 'unknown';
  recordUITraceEntry({ at: Date.now(), type, changed });
  return result;
};
