import assert from 'node:assert/strict';

import { test } from 'vitest';

import { handleAIActivity } from './aiActivityThunks';
import { store } from './store';
import { selectionKey } from './versionSuggestions';

// The ai-activity event carries one AI tab's state as a pair of signals, and
// the defect these tests pin is what a consumer does with only half of it. The
// Go side emits busy and awaitingInput together because they are one state — a
// session is either working or blocked on the operator — so a handler that
// wrote only the field that changed would leave a row spinning under a report
// that says the tool handed control back, and a "waiting on you" marker under
// one that says a turn just started.
//
// The store is the app's own singleton, for the same reason
// closeEnvironmentThunks.test.ts uses it: the reducer under test is the real
// one. Each test leaves the env it touched with neither signal set.

const selection = { tenant: 'signals', environment: 'dev' };
const key = selectionKey(selection);

function signals(): { busy: boolean; awaiting: boolean } {
  const state = store.getState().aiActivity;
  return {
    busy: state.aiBusyByEnv[key] === true,
    awaiting: state.aiAwaitingByEnv[key] === true,
  };
}

test('a turn boundary reads as waiting on the operator, not as a quiet row', () => {
  store.dispatch(handleAIActivity({ sessionId: 7, ...selection, busy: true }));
  assert.deepEqual(signals(), { busy: true, awaiting: false });

  store.dispatch(
    handleAIActivity({ sessionId: 7, ...selection, busy: false, awaitingInput: true }),
  );
  assert.deepEqual(signals(), { busy: false, awaiting: true });

  // The next turn hands control back to the tool, and the marker goes with it.
  store.dispatch(
    handleAIActivity({ sessionId: 7, ...selection, busy: true, awaitingInput: false }),
  );
  assert.deepEqual(signals(), { busy: true, awaiting: false });

  store.dispatch(handleAIActivity({ sessionId: 7, ...selection, busy: false }));
  assert.deepEqual(signals(), { busy: false, awaiting: false });
});

// The volume fallback has nothing to say about awaiting input: it is a guess
// made from output, and a session blocked on a human produces none. An event
// without the field must clear the marker rather than leave a stale one, since
// the Go side emits both signals from every report and an absent field means
// this source is not claiming the state.
test('an event with no awaiting field does not leave a stale marker behind', () => {
  store.dispatch(
    handleAIActivity({ sessionId: 7, ...selection, busy: false, awaitingInput: true }),
  );
  assert.deepEqual(signals(), { busy: false, awaiting: true });

  store.dispatch(handleAIActivity({ sessionId: 7, ...selection, busy: true }));
  assert.deepEqual(signals(), { busy: true, awaiting: false });

  store.dispatch(handleAIActivity({ sessionId: 7, ...selection, busy: false }));
  assert.deepEqual(signals(), { busy: false, awaiting: false });
});

// An orchestrator session has no env to key by, and its own activity model has
// no blocked-on-the-operator state to report, so an event for it must not
// create an env-keyed entry out of an empty tenant.
test('an orchestrator event never touches the env-keyed signals', () => {
  store.dispatch(
    handleAIActivity({
      sessionId: 8,
      tenant: '',
      environment: '',
      busy: true,
      awaitingInput: true,
    }),
  );
  assert.deepEqual(signals(), { busy: false, awaiting: false });
  assert.equal(store.getState().aiActivity.aiBusyBySession[8], true);
  store.dispatch(handleAIActivity({ sessionId: 8, tenant: '', environment: '', busy: false }));
  assert.equal(store.getState().aiActivity.aiBusyBySession[8], undefined);
});
