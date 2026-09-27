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
        // poll, say) resolves harmlessly instead of rejecting: this test is
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

// kind 'erun' seeds the tab whose session carries the env's openSelection;
// 'local' seeds one of the env's default tabs that register none
// (spawnDefaultTab), which is still a session the close kills.
function seedOpenEnv(kind: 'erun' | 'local' = 'erun'): {
  selection: UISelection;
  key: string;
  sessionId: number;
} {
  const selection: UISelection = { tenant: 'pw', environment: 'alpha' };
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
  } as unknown as TerminalController;
  return { selection, key, sessionId };
}

function notice(): { message: string; kind: string } {
  return {
    message: store.getState().terminalStatus.terminalMessage,
    kind: store.getState().terminalStatus.terminalStatusKind,
  };
}

test('a close the operator asked for does not report the exit it caused', async () => {
  const calls = stubCloseRpc();
  const { selection, key, sessionId } = seedOpenEnv();

  const closing = dispatch(closeEnvironment(selection));
  // The desktop's teardown kills the PTY while the RPC is still in flight, so
  // the exit lands here — between markEnvClosing and the close's own tail.
  dispatch(handleTerminalExit({ sessionId, reason: 'signal: killed' }));

  assert.equal(
    notice().message,
    '',
    `a close that succeeded must not render the killed PTY as a failure (got ${JSON.stringify(notice())})`,
  );

  calls[0]?.resolve([sessionId]);
  await closing;
  assert.ok(
    !store.getState().sessions.closingEnvs[key],
    'the close released its hold on the env once its tabs were gone',
  );
  assert.equal(notice().kind, 'info');
});

// The operator's own close can be in flight more than once for one env — a
// second trigger (or a retrying affordance) dispatches it again while the
// first RPC has not returned. The env is still being torn down until the LAST
// of those settles, so an exit arriving in between is still that teardown's
// own exit; a close that settles first — here a failed one, the transport
// fault the close-failure spec already stages — must not release the hold the
// still-running close is relying on.
test('a close that settles first does not release the hold a second close is still using', async () => {
  const calls = stubCloseRpc();
  const { selection, sessionId } = seedOpenEnv();

  const first = dispatch(closeEnvironment(selection));
  const second = dispatch(closeEnvironment(selection));
  calls[0]?.reject(new Error('CLOSE_ENVIRONMENT_UNREACHABLE'));
  await first;

  dispatch(handleTerminalExit({ sessionId, reason: 'signal: killed' }));

  assert.doesNotMatch(
    notice().message,
    /signal: killed/,
    'the still-running close owns this exit, not an unexpected death to report',
  );

  calls[1]?.resolve([sessionId]);
  await second;
});

// The env's ERun tab is not the only session a close kills, and it is not the
// only one that can own the pane. Its Local and AI tabs are killed by the same
// teardown but register no openSelection, so their exit carries the reason on
// its own ("signal: killed"), not wrapped as a failure — and rendering that for
// a close the operator just watched succeed says the same nothing.
test('a close reports nothing for the tabs that carry no open selection', async () => {
  const calls = stubCloseRpc();
  const { selection, sessionId } = seedOpenEnv('local');

  const closing = dispatch(closeEnvironment(selection));
  dispatch(handleTerminalExit({ sessionId, reason: 'signal: killed' }));

  assert.equal(
    notice().message,
    '',
    `a close that succeeded must not report its own kill for the env's other tabs (got ${JSON.stringify(notice())})`,
  );

  calls[0]?.resolve([sessionId]);
  await closing;
});

test('a session that dies with no close in flight still reports the failure', () => {
  const { sessionId } = seedOpenEnv();

  dispatch(handleTerminalExit({ sessionId, reason: 'signal: killed' }));

  assert.equal(notice().kind, 'error');
  assert.match(notice().message, /signal: killed/);
});

// The same contrast for the other branch: reading the env off the tab strip is
// only ever allowed to silence an exit while that env is being closed, never a
// session that died on its own.
test('a tab with no open selection that dies on its own still reports the exit', () => {
  const { sessionId } = seedOpenEnv('local');

  dispatch(handleTerminalExit({ sessionId, reason: 'signal: killed' }));

  assert.match(notice().message, /signal: killed/);
});
