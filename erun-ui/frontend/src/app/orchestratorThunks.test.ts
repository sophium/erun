import assert from 'node:assert/strict';

import { test } from 'vitest';

import { openOrchestrator } from './orchestratorThunks';
import { setSelected } from './slices/selectionSlice';
import { resetTenantDashboard } from './slices/tenantDashboardSlice';
import { setSessionId } from './slices/terminalSlice';
import type { AppThunk } from './store';

interface RecordedAction {
  type: string;
  payload?: unknown;
}
type ThunkGetState = Parameters<AppThunk>[1];
type ThunkExtraArg = Parameters<AppThunk>[2];
type LooseDispatch = (action: unknown) => unknown;

// Unwraps nested thunks as redux-thunk would, recording only plain actions.
function collectDispatched(thunk: AppThunk): RecordedAction[] {
  const actions: RecordedAction[] = [];
  const getState = (() => ({})) as ThunkGetState;
  const extra = undefined as unknown as ThunkExtraArg;
  const dispatch: LooseDispatch = (action) => {
    if (typeof action === 'function') {
      return (action as (d: LooseDispatch, g: ThunkGetState, e: ThunkExtraArg) => unknown)(
        dispatch,
        getState,
        extra,
      );
    }
    actions.push(action as RecordedAction);
    return undefined;
  };
  thunk(dispatch, getState, extra);
  return actions;
}

test('opening a running orchestrator clears the tenant dashboard, not just the session id', () => {
  const actions = collectDispatched(openOrchestrator(42));
  const types = actions.map((action) => action.type);

  assert.ok(
    types.includes(resetTenantDashboard.type),
    `expected the tenant dashboard to be cleared so the pane can show the orchestrator; dispatched: ${types.join(', ')}`,
  );
  assert.ok(types.includes(setSelected.type), 'expected the stale environment selection to clear');
  const sessionAction = actions.find((action) => action.type === setSessionId.type);
  assert.ok(sessionAction, 'expected the session to be focused');
  assert.equal(sessionAction.payload, 42);
});

test('opening a stopped orchestrator (sessionId 0) dispatches nothing', () => {
  const actions = collectDispatched(openOrchestrator(0));
  assert.deepEqual(actions, []);
});
