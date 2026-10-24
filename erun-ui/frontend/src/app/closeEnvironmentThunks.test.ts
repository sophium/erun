import assert from 'node:assert/strict';

import { test } from 'vitest';

import type { UISelection } from '@/types';

import { closeEnvironment } from './closeEnvironmentThunks';
import { hideTerminalMessage } from './notificationThunks';
import { setSelected } from './slices/selectionSlice';
import { trackOpenSession } from './slices/sessionsSlice';
import { setSessionId } from './slices/terminalSlice';
import { store } from './store';
import { recordTab } from './tabsThunks';
import type { TerminalController } from './TerminalController';
import { TerminalSessionRegistry } from './TerminalSessionRegistry';
import { thunkExtra } from './thunkExtra';
import { selectionKey } from './versionSuggestions';
import { handleTerminalExit } from './wailsEventThunks';

// Closing an env kills its PTYs; whether an exit is the close's own comes from
// the backend's deliberate mark on the exit, never from a close-time snapshot.

// handleTerminalExit reads the app's store singleton, so tests drive that store.
const dispatch = store.dispatch;

interface PendingClose {
  resolve: (closed: number[]) => void;
  reject: (error: Error) => void;
}

// Each close RPC parks until resolved, holding the close window open.
function stubCloseRpc(): PendingClose[] {
  const calls: PendingClose[] = [];
  const app = {
    CloseEnvironmentSessions: () =>
      new Promise<number[]>((resolve, reject) => {
        calls.push({ resolve, reject });
      }),
  };
  (globalThis as unknown as { window: unknown }).window = {
    go: {
      main: {
        // Other bindings the close reaches resolve harmlessly.
        App: new Proxy(app, {
          get: (target: typeof app, property: string) =>
            property in target ? target[property as keyof typeof app] : () => Promise.resolve(null),
        }),
      },
    },
  };
  return calls;
}

let nextSessionId = 9100;

// seedOpenEnv seeds one open env with `kind`'s session on the pane. Keys must
// be unique per test: an unsettled close leaves its in-flight count behind.
function seedOpenEnv(
  environment: string,
  kind: 'erun' | 'local' = 'erun',
): {
  selection: UISelection;
  key: string;
  sessionId: number;
} {
  const selection: UISelection = { tenant: 'pw', environment };
  const key = selectionKey(selection);
  const sessionId = nextSessionId;
  nextSessionId += 1;
  if (kind === 'erun') {
    dispatch(trackOpenSession({ key, sessionId, selection }));
  }
  const label = kind === 'erun' ? 'ERun' : 'Local';
  dispatch(recordTab(key, sessionId, 0, kind, label));
  dispatch(setSelected(selection));
  dispatch(setSessionId(sessionId));
  dispatch(hideTerminalMessage());
  thunkExtra.controller = {
    sessions: new TerminalSessionRegistry(),
    scheduleIdleStatusPoll: () => undefined,
    // terminalDisplayMiddleware calls these on every setSessionId.
    snapshotSession: () => undefined,
    resetTerminal: () => undefined,
    activateSession: () => undefined,
    resizeActiveSession: () => undefined,
    focusTerminalSoon: () => undefined,
    queueTerminalResize: () => undefined,
  } as unknown as TerminalController;
  return { selection, key, sessionId };
}

// recordSpawnedTab models a session the store learns of only after its spawn resolves.
function recordSpawnedTab(key: string, kind: 'local' | 'extra' = 'local'): number {
  const sessionId = nextSessionId;
  nextSessionId += 1;
  dispatch(recordTab(key, sessionId, 1, kind, kind === 'local' ? 'Local' : 'Terminal 1'));
  return sessionId;
}

function notice(): { message: string; kind: string } {
  return {
    message: store.getState().terminalStatus.terminalMessage,
    kind: store.getState().terminalStatus.terminalStatusKind,
  };
}

function envHolds(key: string): boolean {
  return (store.getState().sessions.closingEnvs[key] ?? 0) > 0;
}

test('a close the operator asked for does not report the exit it caused', async () => {
  const calls = stubCloseRpc();
  const { selection, key, sessionId } = seedOpenEnv('succeeds');

  const closing = dispatch(closeEnvironment(selection));
  // The exit lands while the close RPC is still in flight.
  dispatch(handleTerminalExit({ sessionId, reason: 'signal: killed', deliberate: true }));

  assert.equal(
    notice().message,
    '',
    `a close that succeeded must not render the killed PTY as a failure (got ${JSON.stringify(notice())})`,
  );

  calls[0]?.resolve([sessionId]);
  await closing;
  assert.ok(!envHolds(key), 'the close released its hold on the env once its tabs were gone');
  assert.equal(notice().kind, 'info');
});

test('a close reports nothing for the tabs that carry no open selection', async () => {
  const calls = stubCloseRpc();
  const { selection, sessionId } = seedOpenEnv('unwrapped', 'local');

  const closing = dispatch(closeEnvironment(selection));
  dispatch(handleTerminalExit({ sessionId, reason: 'signal: killed', deliberate: true }));

  assert.equal(
    notice().message,
    '',
    `a close that succeeded must not report its own kill for the env's other tabs (got ${JSON.stringify(notice())})`,
  );

  calls[0]?.resolve([sessionId]);
  await closing;
});

test('a close that settles first does not release the hold a second close is still using', async () => {
  const calls = stubCloseRpc();
  const { selection, key } = seedOpenEnv('overlapping');

  const first = dispatch(closeEnvironment(selection));
  const second = dispatch(closeEnvironment(selection));
  calls[0]?.reject(new Error('CLOSE_ENVIRONMENT_UNREACHABLE'));
  await first;

  assert.ok(envHolds(key), 'the env is still mid-teardown while the second close has not settled');

  calls[1]?.resolve([]);
  await second;
  assert.ok(!envHolds(key), 'the last close to settle releases the env');
});

// The backend continues past a session whose Close fails, so a failed close
// may still have killed sessions.
test('a close that fails still owns the exits it killed on the way down', async () => {
  const calls = stubCloseRpc();
  const { selection, sessionId } = seedOpenEnv('partial-kill');

  const closing = dispatch(closeEnvironment(selection));
  calls[0]?.reject(new Error('CLOSE_ENVIRONMENT_PARTIAL'));
  await closing;
  // The close's own failure notice stands; the exit must not add to it.
  const afterClose = notice();

  dispatch(handleTerminalExit({ sessionId, reason: 'signal: killed', deliberate: true }));

  assert.deepEqual(
    notice(),
    afterClose,
    `a close that failed part-way must not report the exits it killed on the way (got ${JSON.stringify(notice())})`,
  );
});

// A rejection may come before any session was reached.
test('a failed close that killed nothing does not silence a later death', async () => {
  const calls = stubCloseRpc();
  const { selection, sessionId } = seedOpenEnv('nothing-killed');

  const closing = dispatch(closeEnvironment(selection));
  calls[0]?.reject(new Error('CLOSE_ENVIRONMENT_UNREACHABLE'));
  await closing;

  dispatch(handleTerminalExit({ sessionId, reason: 'exit status 1' }));

  assert.match(
    notice().message,
    /exit status 1/,
    `a close that killed nothing must not answer for a later death (got ${JSON.stringify(notice())})`,
  );
});

test('a failed close does not silence a death of a session it never killed', async () => {
  const calls = stubCloseRpc();
  const { selection, key, sessionId } = seedOpenEnv('partial-survivor', 'local');
  const killed = recordSpawnedTab(key, 'extra');

  const closing = dispatch(closeEnvironment(selection));
  calls[0]?.reject(new Error('CLOSE_ENVIRONMENT_PARTIAL'));
  await closing;

  dispatch(handleTerminalExit({ sessionId: killed, reason: 'signal: killed', deliberate: true }));
  dispatch(handleTerminalExit({ sessionId, reason: 'exit status 1' }));

  assert.match(
    notice().message,
    /exit status 1/,
    `a failed close answers for its own kills, never for the env (got ${JSON.stringify(notice())})`,
  );
});

test('a session the close killed before it reached the tab strip is still its own exit', async () => {
  const calls = stubCloseRpc();
  const { selection, key } = seedOpenEnv('unrecorded', 'local');

  const closing = dispatch(closeEnvironment(selection));
  calls[0]?.reject(new Error('CLOSE_ENVIRONMENT_PARTIAL'));
  await closing;
  const afterClose = notice();

  const landed = recordSpawnedTab(key, 'extra');
  dispatch(setSessionId(landed));
  dispatch(handleTerminalExit({ sessionId: landed, reason: 'signal: killed', deliberate: true }));

  assert.deepEqual(
    notice(),
    afterClose,
    `a session the close killed must be owned even with no tab to have named it (got ${JSON.stringify(notice())})`,
  );
});

// closingEnvs is decremented only on settle, so a never-settling close holds
// its count; exits without a deliberate mark must still be reported.
test('a close that never settles does not silence a death it did not cause', () => {
  stubCloseRpc();
  const { selection, key, sessionId } = seedOpenEnv('never-settles');

  void dispatch(closeEnvironment(selection));

  dispatch(handleTerminalExit({ sessionId, reason: 'exit status 1' }));

  assert.match(
    notice().message,
    /exit status 1/,
    `a close in flight answers for its own kills, not for a death it did not cause (got ${JSON.stringify(notice())})`,
  );
  assert.ok(
    envHolds(key),
    'the unsettled close is still holding the env, and only a clock could release it',
  );
});

test('a session that dies with no close in flight still reports the failure', () => {
  const { sessionId } = seedOpenEnv('unrelated-death');

  dispatch(handleTerminalExit({ sessionId, reason: 'signal: killed' }));

  assert.equal(notice().kind, 'error');
  assert.match(notice().message, /signal: killed/);
});

test('a tab with no open selection that dies on its own still reports the exit', () => {
  const { sessionId } = seedOpenEnv('unrelated-local-death', 'local');

  dispatch(handleTerminalExit({ sessionId, reason: 'signal: killed' }));

  assert.match(notice().message, /signal: killed/);
});
