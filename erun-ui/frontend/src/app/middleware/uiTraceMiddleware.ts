import type { Middleware } from '@reduxjs/toolkit';

import { recordUITraceEntry } from '../uiTraceBuffer';

// uiTraceMiddleware feeds the Diagnostics console's "UI trace" tab. The
// per-action slice comparison stays shallow because it runs on the hot
// dispatch path.
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
