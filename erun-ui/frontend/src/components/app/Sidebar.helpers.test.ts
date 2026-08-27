import assert from 'node:assert/strict';
import { test } from 'node:test';

import { deriveEnvironmentRow, environmentIndicator } from './Sidebar.helpers';

// One derived row state from three inputs: the sticky condition the desktop set,
// what the environment reports about itself, and whether the desktop owns tabs.
// The cases below are the five an operator has to be able to tell apart —
// stopped, unhealthy, busy, reachable-but-not-opened, and opened-and-quiet —
// plus the never-opened row, which must stay blank.

const base = {
  name: 'team / dev',
  envState: '',
  isOpen: false,
  reachable: false,
  outage: false,
  busy: false,
  detail: '',
};

test('a never-opened, unreachable environment renders no indicator', () => {
  const indicator = environmentIndicator(base);
  assert.equal(indicator.visible, false);
  assert.equal(indicator.opened, false);
  // The hover card still has a row to fill, and it must not claim the
  // environment is idle — a never-opened env has no pod to be idle.
  assert.equal(indicator.activity, 'Not open');
});

test('an environment reachable from the CLI is visible without being opened here', () => {
  // The half of the bug that made a CLI-driven env render blank: its forwards
  // are up and its edge answers, but the desktop owns no tabs for it.
  const indicator = environmentIndicator({ ...base, reachable: true });
  assert.equal(indicator.visible, true);
  assert.equal(indicator.dot, 'running');
  assert.equal(indicator.opened, false);
  assert.match(indicator.condition, /in use elsewhere — not opened here/);
  assert.equal(indicator.activity, 'In use elsewhere — not opened here');
});

test('an environment the desktop opened is a close control, not a status light', () => {
  const indicator = environmentIndicator({ ...base, isOpen: true, reachable: true });
  assert.equal(indicator.opened, true);
  assert.equal(indicator.dot, 'running');
  assert.equal(indicator.condition, 'team / dev is running');
  assert.equal(indicator.activity, 'Idle');
});

test('a busy environment says what is keeping it busy', () => {
  const indicator = environmentIndicator({
    ...base,
    reachable: true,
    busy: true,
    detail: 'holding: gradle-build',
  });
  assert.equal(indicator.dot, 'busy');
  // The indicator's label is read out of context and names the env; the hover
  // card's row sits under a heading that already does.
  assert.equal(indicator.condition, 'team / dev is busy — holding: gradle-build');
  assert.equal(indicator.activity, 'Busy — holding: gradle-build');
});

test('a busy environment with no detail still reads busy', () => {
  const indicator = environmentIndicator({ ...base, reachable: true, busy: true });
  assert.equal(indicator.dot, 'busy');
  assert.equal(indicator.condition, 'team / dev is busy');
});

test('a stopped environment reads stopped even if its last observation was busy', () => {
  // The observation is a poll result that can arrive after the stop; the sticky
  // condition has to win or a stopped row would animate as if it were working.
  for (const envState of ['stopped', 'runtime-stopped']) {
    const indicator = environmentIndicator({ ...base, envState, isOpen: true, busy: true });
    assert.equal(indicator.dot, 'stopped', envState);
    assert.match(indicator.condition, /is stopped — /, envState);
    assert.match(indicator.activity, /^Stopped — /, envState);
  }
});

test('an unhealthy environment reads failed, never busy', () => {
  const indicator = environmentIndicator({
    ...base,
    envState: 'failed',
    isOpen: true,
    busy: true,
    detail: 'holding: agent-run',
  });
  assert.equal(indicator.dot, 'failed');
  assert.match(indicator.condition, /deploy failed — /);
  assert.equal(indicator.activity, 'Deploy failed — recover from Activities');
});

test('a stopped sticky condition does not outlive the session that produced it', () => {
  // Closing an environment can leave a last-known condition behind. With no
  // tabs there is no session for it to describe and no way to act on it, so the
  // row goes quiet rather than flying a stale stopped ring. A failed deploy is
  // the exception — see the next test.
  for (const envState of ['stopped', 'runtime-stopped']) {
    const indicator = environmentIndicator({ ...base, envState });
    assert.equal(indicator.visible, false, envState);
  }
});

test('a failed deploy stays visible after its session closes, unless the environment is reachable again', () => {
  // Unlike stopped/runtime-stopped, a failed deploy describes the environment
  // itself (its runtime is broken), not the session that watched it fail —
  // closing the tabs that observed the failure does not fix it. Before this,
  // the row went silently blank, indistinguishable from one nobody had ever
  // opened.
  const stillBroken = environmentIndicator({ ...base, envState: 'failed' });
  assert.equal(stillBroken.visible, true);
  assert.equal(stillBroken.dot, 'failed');
  assert.equal(stillBroken.opened, false);
  assert.match(stillBroken.condition, /deploy failed — /);
  assert.equal(stillBroken.activity, 'Deploy failed — recover from Activities');

  // Once the environment answers again, the stale flag no longer wins — see
  // 'a reachable environment is reported on its own terms' below.
  const recovered = environmentIndicator({ ...base, envState: 'failed', reachable: true });
  assert.equal(recovered.dot, 'running');
});

test('a bound-but-dead forward is reported as an outage, not as a quiet row', () => {
  // The bound-but-dead row: the environment's port-forward still holds its
  // local port, so every check that stops at the listener calls it reachable,
  // and the desktop has no tabs for it. Rendered from reachable alone it is a
  // green "in use elsewhere" light on an environment no client can talk to.
  const indicator = environmentIndicator({ ...base, reachable: true, outage: true });
  assert.equal(indicator.visible, true);
  assert.equal(indicator.dot, 'failed');
  assert.equal(
    indicator.condition,
    'team / dev is unreachable — its connection is dead; deploy it to bring the runtime back',
  );
  assert.equal(
    indicator.activity,
    'Unreachable — its connection is dead; deploy it to bring the runtime back',
  );
});

test('a dropped forward is an outage, not the blank row of an environment nobody opened', () => {
  // The ordinary shape: a pod replacement makes kubectl exit, so the port is
  // free and the environment is not reachable at all. Every field except this
  // one then reads exactly like an environment nobody ever opened, so the row
  // has to be carried by the diagnosis alone — and it must still name the
  // recovery, since being unreachable is the whole of what the operator sees.
  const indicator = environmentIndicator({ ...base, reachable: false, outage: true });
  assert.equal(indicator.visible, true);
  assert.equal(indicator.dot, 'failed');
  assert.equal(
    indicator.condition,
    'team / dev is unreachable — its connection is dead; deploy it to bring the runtime back',
  );
  assert.equal(
    indicator.activity,
    'Unreachable — its connection is dead; deploy it to bring the runtime back',
  );
});

test('a reachable environment is reported on its own terms, not a sticky condition', () => {
  const indicator = environmentIndicator({ ...base, envState: 'failed', reachable: true });
  assert.equal(indicator.visible, true);
  assert.equal(indicator.dot, 'running');
  assert.equal(indicator.activity, 'In use elsewhere — not opened here');
});

// The row's spinner is a separate question from the status dot: the dot carries
// the environment's condition, the spinner carries whether it is working.
//
// Four of the five inputs are desktop-local — they report what this desktop
// launched. The fifth is what the environment says about itself, and it is the
// only one true regardless of who started the work. It was selected,
// destructured, and then not passed in, so an environment driven from a
// terminal, by an orchestrator over MCP, or by a detached job did real work
// behind a row that looked idle.

function rowArgs(overrides: {
  isOpening?: boolean;
  runningCommand?: string;
  aiState?: string;
  reconnecting?: boolean;
  envBusy?: boolean;
  envBusyDetail?: string;
  envObserved?: boolean;
}) {
  return {
    isOpening: false,
    runningCommand: '',
    aiState: undefined,
    reconnecting: false,
    envBusy: false,
    envBusyDetail: '',
    // Unobserved by default: no answer from the environment must not clear
    // anything, so a test that says nothing about it gets the old behaviour.
    envObserved: false,
    ...overrides,
  };
}

function row(overrides: Parameters<typeof rowArgs>[0]) {
  const input = rowArgs(overrides);
  return deriveEnvironmentRow(
    'team',
    'dev',
    null,
    [],
    input.isOpening,
    input.runningCommand,
    input.aiState,
    input.reconnecting,
    input.envBusy,
    input.envBusyDetail,
    input.envObserved,
  );
}

test('an environment working for reasons this desktop did not start still spins', () => {
  const derived = row({ envBusy: true, envBusyDetail: 'erun release 1.0.176' });
  assert.equal(derived.busy, true);
  // The lease names the work, and that is the only signal saying what is
  // actually running — so it belongs in the label rather than a generic "busy".
  assert.equal(derived.busyLabel, 'team / dev is busy — erun release 1.0.176');
});

test('an environment reporting itself busy without a detail still spins', () => {
  const derived = row({ envBusy: true });
  assert.equal(derived.busy, true);
  assert.equal(derived.busyLabel, 'team / dev is busy');
});

test('a quiet environment nobody is driving does not spin', () => {
  const derived = row({});
  assert.equal(derived.busy, false);
  assert.equal(derived.busyLabel, '');
});

test('a command this desktop started keeps its own more specific label', () => {
  // The desktop knows what it launched; the environment only knows that
  // something holds it. The specific answer wins when both are true.
  const derived = row({ runningCommand: 'deploy', envBusy: true, envBusyDetail: 'a lease' });
  assert.equal(derived.busy, true);
  assert.match(derived.busyLabel, /team \/ dev$/);
  assert.doesNotMatch(derived.busyLabel, /a lease/);
});

test('each desktop-local reason still spins its own row on its own', () => {
  for (const overrides of [
    { isOpening: true },
    { runningCommand: 'deploy' },
    { aiState: 'busy' },
    { reconnecting: true },
  ]) {
    assert.equal(row(overrides).busy, true, `expected busy for ${JSON.stringify(overrides)}`);
  }
});

// The defect: runningCommand is desktop-local, set when this desktop starts
// something and cleared when it sees it end. A command driven from a terminal
// or over MCP, or a session that goes away, leaves a latch nobody can clear —
// one row span for six hours while the environment reported every marker idle.
test('an environment that reports itself idle clears a stale desktop latch', () => {
  const stuckCommand = row({ runningCommand: 'deploy', envObserved: true, envBusy: false });
  assert.equal(stuckCommand.busy, false);
});

// aiState is not a desktop-local latch the way runningCommand is: it comes
// from the same environment-activity observation as envBusy/envObserved, so
// it is trusted unconditionally rather than gated behind envObserved.
test("the AI session's own busy/awaiting-input state is trusted even when the generic markers read idle", () => {
  assert.equal(row({ aiState: 'busy', envObserved: true, envBusy: false }).busy, true);
  assert.equal(row({ aiState: 'awaiting-input', envObserved: true, envBusy: false }).busy, true);
  assert.equal(
    row({ aiState: 'awaiting-input', envObserved: true, envBusy: false }).awaitingInput,
    true,
  );
  assert.equal(row({ aiState: 'idle', envObserved: true, envBusy: false }).busy, false);
  assert.equal(row({ aiState: 'unknown', envObserved: true, envBusy: false }).busy, false);
});

// Silence is not an answer. This covers both an environment nobody reached and
// one whose port answers while its edge has wedged — the poller reports no work
// in both cases because nobody got a verdict, and neither may clear a latch.
test('an environment that gave no verdict keeps its desktop latch', () => {
  assert.equal(row({ runningCommand: 'deploy', envObserved: false }).busy, true);
});

// The environment's own answer still wins upward, whoever started the work.
test('an environment that reports work spins even when the desktop started nothing', () => {
  assert.equal(row({ envBusy: true, envObserved: true }).busy, true);
});

// This desktop's own in-flight operations answer for themselves: the operator
// clicked a moment ago and the environment cannot yet have observed it, so a
// reachable-idle report must not swallow the feedback for the click.
test("an idle report does not swallow this desktop's in-flight operation", () => {
  assert.equal(row({ isOpening: true, envObserved: true, envBusy: false }).busy, true);
  assert.equal(row({ reconnecting: true, envObserved: true, envBusy: false }).busy, true);
});

// The row's spinner label names its environment because a screen reader has no
// other context for it. A hover card headed with that same environment does, so
// it needs to know which kind of label it was handed rather than repeating
// "<env> is busy — X" under a heading that already says "<env>".
test('a label describing the environment is marked as the environment own', () => {
  const derived = row({ envBusy: true, envBusyDetail: 'holding: gradle-build', envObserved: true });
  assert.equal(derived.busyLabel, 'team / dev is busy — holding: gradle-build');
  assert.equal(derived.busyFromEnvironment, true);
});

// awaiting-input gets its own wording, distinct from the generic "AI tab
// working" busy label — the operator needs to see that the Agent is stuck on
// them, not merely that it is running.
test('the busy label names awaiting-input distinctly from busy', () => {
  assert.equal(
    row({ aiState: 'awaiting-input' }).busyLabel,
    'Agent is waiting for your input in team / dev',
  );
  assert.equal(row({ aiState: 'busy' }).busyLabel, 'AI tab working on team / dev');
});

test('a label describing an operation this desktop is running is not', () => {
  // A command this desktop is running names the row, so the label is about the
  // desktop even when the environment reports busy at the same moment.
  const withCommand = row({ runningCommand: 'build', envBusy: true, envObserved: true });
  assert.equal(withCommand.busyLabel, 'Building team / dev');
  assert.equal(withCommand.busyFromEnvironment, false);

  assert.equal(row({ isOpening: true, envBusy: true }).busyFromEnvironment, false);
  assert.equal(row({ reconnecting: true, envBusy: true }).busyFromEnvironment, false);
});
