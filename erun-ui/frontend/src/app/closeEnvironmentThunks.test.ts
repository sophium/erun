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

// Closing an env tears its PTYs down on purpose: CloseEnvironmentSessions
// SIGKILLs them, a reader parked in Read wakes on that kill with a non-empty
// reason ("signal: killed"), and handleTerminalExit used to render the exit it
// had just caused back to the operator as "Failed to open <tenant> / <env>:
// signal: killed" (or as the bare reason on the env's Local and AI tabs).
//
// These tests pin the rule that decides what that exit is. Ownership is asked
// of the exit itself — the backend marks a session torn-down before it touches
// the PTY and carries that on the exit (terminalExitPayload.Deliberate) — and
// never of a set the desktop snapshotted at close time. A snapshot cannot
// answer the question in either direction: the set a close reaches is not the
// set it set out to walk (it continues past a session whose Close fails and
// comes back an error with the rest already dead), and a session it kills
// before the frontend has recorded a tab for it appears in no snapshot at all.
// Each test therefore names the exits the backend would flag, and asserts on
// what the operator is told about them.

// The registry behind handleTerminalExit reads the app's own store singleton,
// so these tests drive that store rather than a locally built one: the
// exit-selection bookkeeping under test is the real one.
const dispatch = store.dispatch;

interface PendingClose {
  resolve: (closed: number[]) => void;
  reject: (error: Error) => void;
}

// Every CloseEnvironmentSessions call parks until the test resolves it, so the
// window between "the desktop started killing this env's PTYs" and "the RPC
// came back" is held open and observable — the window the operator's own close
// passes through.
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
        // Any other binding the close's own tail incidentally reaches (an idle
        // poll, say) resolves harmlessly instead of rejecting: these tests are
        // about the terminal-exit feedback, not about those reads.
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

// seedOpenEnv seeds one open env and puts `kind`'s session in charge of the
// pane. Every test names its own environment: a close whose RPC never settles
// leaves the env's in-flight count standing, and a shared key would hand that
// to the next test.
//
// kind 'erun' seeds the tab whose session carries the env's openSelection;
// 'local' seeds one of the env's default tabs that registers none
// (spawnDefaultTab), whose exit reaches the feedback path unwrapped.
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
    // refreshIdleStatus re-arms its own poll through the controller; the app's
    // real one would schedule a timer this test would then have to chase.
    scheduleIdleStatusPoll: () => undefined,
    // terminalDisplayMiddleware drives the pane off every setSessionId, so a
    // stub without these throws inside the dispatch it is listening to and
    // buries whatever the test was actually asserting under a listener error.
    snapshotSession: () => undefined,
    resetTerminal: () => undefined,
    activateSession: () => undefined,
    resizeActiveSession: () => undefined,
    // An exit that leaves the exit handler on the auto-select path (@see
    // selectTerminalTab) focuses the pane it lands on.
    focusTerminalSoon: () => undefined,
    queueTerminalResize: () => undefined,
  } as unknown as TerminalController;
  return { selection, key, sessionId };
}

// recordSpawnedTab is a session the frontend only learns the identity of once
// the spawn it was already awaiting resolves — the shape spawnDefaultTab has
// (await the Go call, then recordTab), during which the backend holds a live
// session this store has never heard of.
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

// envHolds reads the env's in-flight close count straight off the store, so
// the probes below assert on the state the closes keep rather than on the
// helper the production reader goes through.
function envHolds(key: string): boolean {
  return (store.getState().sessions.closingEnvs[key] ?? 0) > 0;
}

test('a close the operator asked for does not report the exit it caused', async () => {
  const calls = stubCloseRpc();
  const { selection, key, sessionId } = seedOpenEnv('succeeds');

  const closing = dispatch(closeEnvironment(selection));
  // The desktop's teardown kills the PTY while the RPC is still in flight, so
  // the exit lands here — between markEnvClosing and the close's own tail.
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

// The env's ERun tab is not the only session a close kills, and it is not the
// only one that can own the pane. Its Local and AI tabs are killed by the same
// teardown but register no openSelection, so their exit carries the reason on
// its own ("signal: killed") rather than wrapped as a failure — and rendering
// that for a close the operator just watched succeed says the same nothing.
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

// The operator's own close can be in flight more than once for one env — a
// second trigger, or a retrying affordance. The env is still being torn down
// until the LAST of those settles, so the close that settles first must not
// release the hold the still-running one is relying on.
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

// A close does not have to succeed to have killed things. The backend's
// closeManagedTerminals walks the env's sessions and `continue`s past one whose
// Close reports an error, so the RPC returning an error says only that the walk
// gave up part-way — every session it had already reached is dead or dying and
// its exit is still on its way here. The exit below is the reported shape: it
// carries the backend's mark, and it lands after the RPC settled.
test('a close that fails still owns the exits it killed on the way down', async () => {
  const calls = stubCloseRpc();
  const { selection, sessionId } = seedOpenEnv('partial-kill');

  const closing = dispatch(closeEnvironment(selection));
  calls[0]?.reject(new Error('CLOSE_ENVIRONMENT_PARTIAL'));
  await closing;
  // A failed close does tell the operator it failed — that notice is the
  // close's own and stands. What the exit must not do is add to it.
  const afterClose = notice();

  dispatch(handleTerminalExit({ sessionId, reason: 'signal: killed', deliberate: true }));

  assert.deepEqual(
    notice(),
    afterClose,
    `a close that failed part-way must not report the exits it killed on the way (got ${JSON.stringify(notice())})`,
  );
});

// ——— the other side of that rule, which is what decides its shape ———

// A rejection is not evidence that anything died. The RPC can refuse before
// the backend reaches a single session (an unreachable transport, a rejected
// target), and a close that killed nothing owns nothing: the session is still
// alive, the operator keeps working in it, and its eventual death is its own
// and must be reported.
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

// And a close that killed something owns exactly that. The failed close leaves
// the env's tabs standing and the operator keeps working in them, so a session
// the walk never reached dies later on its own; the kills that did land must
// not swallow it.
test('a failed close does not silence a death of a session it never killed', async () => {
  const calls = stubCloseRpc();
  const { selection, key, sessionId } = seedOpenEnv('partial-survivor', 'local');
  const killed = recordSpawnedTab(key, 'extra');

  const closing = dispatch(closeEnvironment(selection));
  calls[0]?.reject(new Error('CLOSE_ENVIRONMENT_PARTIAL'));
  await closing;

  // The one the walk reached, on its way down.
  dispatch(handleTerminalExit({ sessionId: killed, reason: 'signal: killed', deliberate: true }));
  // The one it never reached, dying on its own afterwards.
  dispatch(handleTerminalExit({ sessionId, reason: 'exit status 1' }));

  assert.match(
    notice().message,
    /exit status 1/,
    `a failed close answers for its own kills, never for the env (got ${JSON.stringify(notice())})`,
  );
});

// The session the close killed before the frontend had a tab for it. Its
// identity is real — the backend killed it — but the store learns it only when
// the spawn it was already awaiting resolves, which is after the close settled,
// so no set snapshotted at close time could have named it. The exit carries its
// own answer, which is what makes it answerable at all.
test('a session the close killed before it reached the tab strip is still its own exit', async () => {
  const calls = stubCloseRpc();
  const { selection, key } = seedOpenEnv('unrecorded', 'local');

  const closing = dispatch(closeEnvironment(selection));
  calls[0]?.reject(new Error('CLOSE_ENVIRONMENT_PARTIAL'));
  await closing;
  const afterClose = notice();

  // The spawn lands: the tab the store never had is recorded and takes the pane.
  const landed = recordSpawnedTab(key, 'extra');
  dispatch(setSessionId(landed));
  dispatch(handleTerminalExit({ sessionId: landed, reason: 'signal: killed', deliberate: true }));

  assert.deepEqual(
    notice(),
    afterClose,
    `a session the close killed must be owned even with no tab to have named it (got ${JSON.stringify(notice())})`,
  );
});

// The never-settling close. closingEnvs is decremented on settle and nothing
// else, so a CloseEnvironmentSessions that never answers holds its env's
// in-flight count for the life of the window — only a clock could bound that,
// and a clock is the wrong instrument: the window it would fire in is exactly
// the one where clearTabsForEnv may still land, which is the tab-respawn race
// the count exists to stop. So the count stays, and what this pins is that its
// reach is bounded to that race: an exit that carries no mark of its own is
// reported whatever the hold is doing.
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

// The same contrast for the tabs that register no openSelection: their exit
// reaches the feedback path unwrapped, and it is reported all the same.
test('a tab with no open selection that dies on its own still reports the exit', () => {
  const { sessionId } = seedOpenEnv('unrelated-local-death', 'local');

  dispatch(handleTerminalExit({ sessionId, reason: 'signal: killed' }));

  assert.match(notice().message, /signal: killed/);
});
