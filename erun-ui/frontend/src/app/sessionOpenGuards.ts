import type { UISelection } from '@/types';

import { selectActiveSessionOrchestrator } from './selectors';
import type { RootState } from './store';

// isStaleDefaultLandingOpen: boot()'s automatic default-landing open defers to
// an orchestrator session that already owns the terminal. Only boot() marks it;
// a deliberate open must still win.
export function isStaleDefaultLandingOpen(
  getState: () => RootState,
  options: { isDefaultLandingOpen?: boolean },
): boolean {
  if (!options.isDefaultLandingOpen) {
    return false;
  }
  return Boolean(selectActiveSessionOrchestrator(getState()));
}

// createIsCurrentSelection returns a fresh-state predicate for post-await steps.
// deferToActiveOrchestrator (boot only) also treats an active orchestrator
// session as navigated away, since it never sets state.selection.selected.
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
