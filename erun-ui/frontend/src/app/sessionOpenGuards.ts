import type { UISelection } from '@/types';

import { selectActiveSessionOrchestrator } from './selectors';
import type { RootState } from './store';

// isStaleDefaultLandingOpen is boot()'s own marker (isDefaultLandingOpen) for
// its automatic open of the default-landing environment: no one deliberately
// chose it, so an orchestrator session already owning the terminal outranks
// it, before openSelection does anything else -- not just against changes
// that happen while it runs. boot() decides to call openSelection from
// state.selection.selected, which an orchestrator session never touches, so a
// slow preceding await (getInitialState, loadOrchestrators) can let it decide
// to call this well after an orchestrator has already claimed the terminal.
// Every later staleness check in openSelection (stateAfterGate, isCurrentSelection
// in finishOpenSession) only catches a change that happens *during* the call --
// neither asks whether the terminal was already spoken for the moment it
// started, so without this, prepareOpenSelection's unconditional setSelected
// still ran and the selection-sync middleware reconciled terminal.sessionId
// back onto this environment, pruning every other one the orchestrator had
// linked out of loadReviewDiff's target set.
//
// This must NOT apply to every caller: a user deliberately opening a plain
// environment while an orchestrator is focused is the same shape (an
// orchestrator still owns the terminal when the call starts) but is the
// opposite intent -- it must win, not defer. Only boot()'s own call sets
// isDefaultLandingOpen; every other caller (a sidebar row click, a wails
// lifecycle event, a notification action) is unmarked and keeps deferring to
// nothing.
export function isStaleDefaultLandingOpen(
  getState: () => RootState,
  options: { isDefaultLandingOpen?: boolean },
): boolean {
  if (!options.isDefaultLandingOpen) {
    return false;
  }
  return Boolean(selectActiveSessionOrchestrator(getState()));
}

// Returns a predicate that post-await dispatches poll to decide whether the
// user is still on this env or has navigated away. It reads getState()
// afresh each call so it tracks setSelected dispatches that fire between awaits.
//
// deferToActiveOrchestrator (boot()'s isDefaultLandingOpen, threaded through
// from openSelection) makes an active orchestrator session ALSO count as
// "navigated away", even though it never touches state.selection.selected --
// an orchestrator is tracked purely via terminal.sessionId
// (selectActiveSessionOrchestrator), so the sidebar's single-env selection
// can still read as this env long after the user has switched into an
// orchestrator's session. Without this, finishOpenSession's tail
// (registerOpenSessionResult, then restoreSelectedTabForEnv) treated a slow,
// still-in-flight default-env open as current, silently yanking terminal
// focus back from the orchestrator and collapsing loadReviewDiff's target set
// down to this one environment -- pruning every other environment the
// orchestrator had linked. This must stay opt-in: a user deliberately opening
// a plain environment while an orchestrator is focused hits the exact same
// state shape (an orchestrator still owns the terminal) but must win, not
// defer, so only boot()'s own call sets it.
export function createIsCurrentSelection(
  getState: () => RootState,
  selection: UISelection,
  deferToActiveOrchestrator?: boolean,
): () => boolean {
  return () => {
    const state = getState();
    if (deferToActiveOrchestrator && selectActiveSessionOrchestrator(state)) {
      return false;
    }
    const current = state.selection.selected;
    if (current === null) {
      return false;
    }
    return current.tenant === selection.tenant && current.environment === selection.environment;
  };
}
