import assert from 'node:assert/strict';

import { beforeEach, test } from 'vitest';

import { setSessionId } from './slices/terminalSlice';
import { store } from './store';
import { recordTab, removeTab } from './tabsThunks';
import type { TerminalController } from './TerminalController';
import { TerminalSessionRegistry } from './TerminalSessionRegistry';
import { thunkExtra } from './thunkExtra';
import { handleTerminalExit } from './wailsEventThunks';

// Retained output is released only once the session exited and no tab shows it.

const dispatch = store.dispatch;
let sessions: TerminalSessionRegistry;
let nextSessionId = 9600;
let keyCounter = 0;

beforeEach(() => {
  sessions = new TerminalSessionRegistry();
  thunkExtra.controller = {
    sessions,
    scheduleIdleStatusPoll: () => undefined,
    snapshotSession: () => undefined,
    resetTerminal: () => undefined,
    activateSession: () => undefined,
    resizeActiveSession: () => undefined,
    focusTerminalSoon: () => undefined,
    queueTerminalResize: () => undefined,
  } as unknown as TerminalController;
  dispatch(setSessionId(1));
});

function seedTab(): { key: string; sessionId: number } {
  keyCounter += 1;
  const key = `release/${String(keyCounter)}`;
  const sessionId = nextSessionId;
  nextSessionId += 1;
  dispatch(recordTab(key, sessionId, 0, 'local', 'Local'));
  sessions.appendSessionBuffer(sessionId, new TextEncoder().encode('raw\n'));
  sessions.appendDisplayBuffer(sessionId, 'shown\n');
  return { key, sessionId };
}

function exit(sessionId: number): void {
  dispatch(handleTerminalExit({ sessionId, reason: '' }));
}

test('an exit releases the raw output at once, but a dead tab keeps what it shows', () => {
  const { sessionId } = seedTab();

  exit(sessionId);

  assert.deepEqual(sessions.sessionBuffer(sessionId), []);
  assert.deepEqual(sessions.displayBuffer(sessionId), ['shown\n']);
});

test('a respawn that replaces a dead tab releases the dead session', () => {
  const { key, sessionId } = seedTab();
  exit(sessionId);

  dispatch(recordTab(key, nextSessionId, 0, 'local', 'Local'));
  nextSessionId += 1;

  assert.deepEqual(sessions.displayBuffer(sessionId), []);
});

test('a tab removed while its session is alive keeps the output until the exit', () => {
  const { key, sessionId } = seedTab();

  dispatch(removeTab(key, sessionId));
  assert.deepEqual(sessions.displayBuffer(sessionId), ['shown\n']);

  exit(sessionId);
  assert.deepEqual(sessions.displayBuffer(sessionId), []);
});

test('removing the tab of a session that already exited releases it', () => {
  const { key, sessionId } = seedTab();
  exit(sessionId);

  dispatch(removeTab(key, sessionId));

  assert.deepEqual(sessions.displayBuffer(sessionId), []);
});
