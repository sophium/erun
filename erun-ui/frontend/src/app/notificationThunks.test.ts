import assert from 'node:assert/strict';
import { test } from 'node:test';

import { showTerminalError, showTerminalFailure } from './notificationThunks';
import { setTerminalCopyOutput, setTerminalMessage } from './slices/terminalStatusSlice';
import type { AppThunk } from './store';

interface RecordedAction {
  type: string;
  payload?: unknown;
}
type ThunkGetState = Parameters<AppThunk>[1];
type ThunkExtraArg = Parameters<AppThunk>[2];
type LooseDispatch = (action: unknown) => unknown;

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

// A bare backend error carries no captured command output, so the titlebar
// must not offer a Copy action for it — copying the same sentence already
// visible in the pill (Titlebar.Status.tsx renders the Copy button only when
// terminalCopyOutput is non-empty) tells the operator nothing they don't
// already see.
test('showTerminalError sets an error status with no copyable output', () => {
  const actions = collectDispatched(
    showTerminalError('no tenant given for open: pass a tenant explicitly'),
  );

  const messageAction = actions.find((action) => action.type === setTerminalMessage.type);
  assert.ok(messageAction, 'expected a terminal status message');
  assert.deepEqual(messageAction.payload, {
    message: 'no tenant given for open: pass a tenant explicitly',
    busy: false,
    kind: 'error',
    detail: '',
    actionKind: '',
  });

  const copyAction = actions.find((action) => action.type === setTerminalCopyOutput.type);
  assert.ok(copyAction, 'expected a copy-output action');
  assert.equal(
    copyAction.payload,
    '',
    'a bare error must not fake a copy buffer from its own text',
  );
});

// A caller that genuinely captured command output (a failed IDE launch, a
// delete's namespace-cleanup warning) still gets to offer it.
test('showTerminalFailure preserves genuinely captured output', () => {
  const actions = collectDispatched(
    showTerminalFailure(
      'Deleted team / dev.',
      'namespace cleanup failed',
      'Deleted team / dev. namespace cleanup failed',
      '',
      null,
    ),
  );

  const copyAction = actions.find((action) => action.type === setTerminalCopyOutput.type);
  assert.ok(copyAction, 'expected a copy-output action');
  assert.equal(copyAction.payload, 'Deleted team / dev. namespace cleanup failed');
});
