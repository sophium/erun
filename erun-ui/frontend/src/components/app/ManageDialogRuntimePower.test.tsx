import assert from 'node:assert/strict';

import { renderToStaticMarkup } from 'react-dom/server';
import { beforeEach, test, vi } from 'vitest';

import { RUNTIME_RUN_STATE_UNREAD, summarizeRuntimeRunState } from '@/app/runtimeRunState';
import type { AppState } from '@/app/state';
import type { UIRuntimeRunState } from '@/uiRuntimeTypes';

import { RuntimePowerField } from './ManageDialogRuntimePower';

// The Runtime tab's Stop control is where a reported bug lived: it offered Stop
// unconditionally, so pressing it on an already-stopped runtime produced a
// correct no-op the operator could only read as a broken button. These cases
// pin the state the report described — already stopped — alongside the states
// around it, because the failure this suite exists to prevent is the fix
// covering the neighbours and missing the reported one.

const { query } = vi.hoisted(() => ({ query: vi.fn() }));

vi.mock('@/app/api/environmentApi', () => ({
  useGetRuntimeRunStateQuery: (...args: unknown[]): unknown => query(...args) as unknown,
}));

vi.mock('@/app/hooks', () => ({
  useAppDispatch: (): unknown => vi.fn(),
  useAppSelector: (): unknown => undefined,
}));

// The component reaches the stop workflow for its button and the stop
// formatter for its help text. Both are stubbed: the render under test is the
// control's own, and loading the thunks would pull the whole stored app in
// with it.
vi.mock('@/app/manageEnvironmentThunks', () => ({ submitManageStop: (): unknown => vi.fn() }));

vi.mock('@/app/notificationThunks', () => ({ showTerminalError: (): unknown => vi.fn() }));

function dialogState(): AppState['manageDialog'] {
  return {
    selection: { tenant: 'petios', environment: 'local' },
    busy: false,
    configLoading: false,
  } as unknown as AppState['manageDialog'];
}

function renderWith(runState: UIRuntimeRunState | undefined, isFetching = false): string {
  query.mockReturnValue({ data: runState, isFetching });
  return renderToStaticMarkup(<RuntimePowerField dialog={dialogState()} />);
}

beforeEach(() => {
  query.mockReset();
});

const stopped: UIRuntimeRunState = {
  tenant: 'petios',
  environment: 'local',
  present: true,
  desiredReplicas: 0,
  readyReplicas: 0,
  stopped: true,
};

test('an already-stopped runtime says so where the button is, and Stop is not offered', () => {
  const markup = renderWith(stopped);
  // The state is on screen before the click, not discovered by pressing.
  assert.match(markup, /Runtime stopped/);
  assert.match(markup, /Nothing to stop — the runtime already wants no pods\./);
  // And the control that would have been a no-op is disabled.
  assert.match(markup, /id="environment-config-stop"[^>]*disabled=""/);
});

test('a runtime that was never deployed is a different state with its own reason', () => {
  const markup = renderWith({ ...stopped, present: false, stopped: false });
  assert.match(markup, /Runtime not deployed/);
  assert.match(
    markup,
    /Nothing to stop — there is no runtime Deployment for this environment yet\./,
  );
  assert.match(markup, /id="environment-config-stop"[^>]*disabled=""/);
});

test('a running runtime offers Stop and reports what it is running', () => {
  const markup = renderWith({ ...stopped, desiredReplicas: 1, readyReplicas: 1, stopped: false });
  assert.match(markup, /Runtime running — 1 of 1 ready/);
  assert.doesNotMatch(markup, /id="environment-config-stop"[^>]*disabled=""/);
});

// An environment that wants a pod and has none is unhealthy, not stopped. It
// must keep offering Stop (a stop is a legitimate response) without claiming
// the runtime is ready.
test('a runtime wanting a pod it has not got is still offered Stop', () => {
  const markup = renderWith({ ...stopped, desiredReplicas: 1, readyReplicas: 0, stopped: false });
  assert.match(markup, /Runtime running — 0 of 1 ready/);
  assert.doesNotMatch(markup, /id="environment-config-stop"[^>]*disabled=""/);
});

// A failed read must not disable the action: nothing observed says there is
// nothing to stop, and blocking the only control would leave the operator with
// no move at all.
test('an unreadable run state states what could not be read and leaves Stop usable', () => {
  const markup = renderWith({
    tenant: 'petios',
    environment: 'local',
    present: false,
    desiredReplicas: 0,
    readyReplicas: 0,
    stopped: false,
    message:
      "Cannot read this environment's runtime run state: exit status 1: kubectl stub: no cluster in the Playwright harness",
  });
  assert.match(markup, new RegExp(RUNTIME_RUN_STATE_UNREAD));
  // The Go read's own headline, which leads with what could not be read; the
  // apostrophe is HTML-escaped in the markup, hence the split match.
  assert.match(markup, /Cannot read this environment/);
  assert.match(markup, /runtime run state: exit status 1: kubectl stub/);
  assert.doesNotMatch(markup, /Runtime not deployed/);
  assert.doesNotMatch(markup, /id="environment-config-stop"[^>]*disabled=""/);
});

// The stop's scope: it scales the runtime Deployment and nothing else, so the
// help has to say what it will not touch. Naming the components the environment
// deploys is the version of that which the operator can check against the pods
// still standing afterwards.
test('the help names the platform components a stop leaves running', () => {
  const markup = renderWith({
    ...stopped,
    remainingComponents: ['petios-backend-api', 'petios-backend-postgres'],
  });
  assert.match(markup, /Platform components deployed into this environment are not part of it/);
  assert.match(
    markup,
    /keep holding their capacity: petios-backend-api, petios-backend-postgres\./,
  );
});

test('a runtime-only environment states the scope without inventing components', () => {
  const markup = renderWith(stopped);
  assert.match(markup, /platform components deployed into this environment/);
  assert.doesNotMatch(markup, /keep holding their capacity: /);
});

// summarizeRuntimeRunState's own contract, kept beside the render: `checking`
// is neither of the two real states, and an unknown state is never reported as
// "nothing to stop".
test('an unread state is checking, not stopped', () => {
  const checking = summarizeRuntimeRunState(undefined, true);
  assert.equal(checking.checking, true);
  assert.equal(checking.nothingToStop, false);
  const resolved = summarizeRuntimeRunState(stopped, false);
  assert.equal(resolved.checking, false);
  assert.equal(resolved.nothingToStop, true);
});
