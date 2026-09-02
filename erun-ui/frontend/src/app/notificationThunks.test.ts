import assert from 'node:assert/strict';
import { afterEach, beforeEach, mock, test } from 'node:test';

import {
  dismissNotification,
  showNotification,
  showTerminalError,
  showTerminalFailure,
} from './notificationThunks';
import {
  dismissNotification as dismissNotificationAction,
  showNotification as showNotificationAction,
} from './slices/notificationSlice';
import { setTerminalCopyOutput, setTerminalMessage } from './slices/terminalStatusSlice';
import type { AppThunk } from './store';
import { TRANSIENT_DISMISS_MS } from './transientDismissDuration';

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

// showNotification schedules its auto-dismiss via scheduleTransientDismiss
// (window.setTimeout plus window/document focus tracking), so these tests
// need a window/document shim whose timers are the (mockable) Node globals --
// same pattern as terminalFocus.test.ts. The window here always reports
// focused: the focus-pausing behavior itself is transientDismissTimer's own
// unit test, not this thunk's.
function installWindow(): void {
  const windowShim = {
    setTimeout: (handler: () => void, ms?: number) => setTimeout(handler, ms) as unknown as number,
    clearTimeout: (id: number) => {
      clearTimeout(id);
    },
    addEventListener: (): void => undefined,
    removeEventListener: (): void => undefined,
  };
  const documentShim = { hasFocus: () => true };
  (globalThis as unknown as { window: unknown }).window = windowShim;
  (globalThis as unknown as { document: unknown }).document = documentShim;
}

beforeEach(() => {
  installWindow();
  mock.timers.enable({ apis: ['setTimeout'] });
});

afterEach(() => {
  mock.timers.reset();
});

test('showNotification auto-dismisses a success entry after the shared transient duration', () => {
  const actions = collectDispatched(showNotification('success', 'Opened VS Code.'));
  const shown = actions.find((action) => action.type === showNotificationAction.type);
  assert.ok(shown, 'expected the entry to be shown');

  assert.equal(
    actions.some((action) => action.type === dismissNotificationAction.type),
    false,
    'must not dismiss before the timer fires',
  );

  mock.timers.tick(TRANSIENT_DISMISS_MS);

  const dismissed = actions.find((action) => action.type === dismissNotificationAction.type);
  assert.ok(dismissed, 'expected an auto-dismiss after the transient duration');
  const shownPayload = shown.payload as { id: string };
  assert.equal(dismissed.payload, shownPayload.id, 'must dismiss the entry it just showed');
});

test('showNotification never auto-dismisses a warning or an error', () => {
  for (const kind of ['warning', 'error'] as const) {
    const actions = collectDispatched(showNotification(kind, 'Deploy of frs/dev failed.'));
    mock.timers.tick(TRANSIENT_DISMISS_MS * 2);
    assert.equal(
      actions.some((action) => action.type === dismissNotificationAction.type),
      false,
      `${kind} must persist until acknowledged`,
    );
  }
});

test('showNotification stamps a fresh timestamp and dismissed: false on every entry', () => {
  const actions = collectDispatched(
    showNotification('info', 'Starting workspace sync for frs/dev...'),
  );
  const shown = actions.find((action) => action.type === showNotificationAction.type);
  assert.ok(shown);
  const payload = shown.payload as { timestamp: number; dismissed: boolean };
  assert.equal(typeof payload.timestamp, 'number');
  assert.equal(payload.dismissed, false);
});

// dismissNotification only marks the entry read -- see AppNotification's own
// doc comment -- so the message centre dialog can still show it afterwards.
// This locks the thunk's contract at the dispatch layer; notificationSlice's
// own reducer test locks the actual state transition.
test('dismissNotification dispatches the slice action for exactly the given id', () => {
  const actions = collectDispatched(dismissNotification('notification-7'));
  assert.deepEqual(actions, [{ type: dismissNotificationAction.type, payload: 'notification-7' }]);
});
