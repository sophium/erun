import type { UISelection } from '@/types';

import { CloseEnvironmentSessions } from '../../wailsjs/go/main/App';
import { readError } from './errors';
import { showTerminalMessage } from './notificationThunks';
import { setSelected } from './slices/selectionSlice';
import { clearEnvOpening } from './slices/sessionsSlice';
import {
  clearSelectedSessionForEnv,
  clearTabsForEnv,
  setDebugOutput,
  setSessionId,
} from './slices/terminalSlice';
import type { AppThunk } from './store';
import { selectionKey } from './versionSuggestions';

// closeEnvironment closes every PTY session bound to (tenant, env)
// on the Go side, then drops the env's tab strip + remembered tab
// + in-flight opening marker on the frontend, and clears
// state.selection.selected when it points at this env. Used by the
// sidebar's "open env" dot — clicking the dot tears down the env's
// Local / ERun / AI tabs and stops the desktop from tracking the
// env in its session state.
//
// Independent of the cloud-context Stop button: closing the env's
// tabs is a desktop-only operation and does NOT touch AWS state.
// Per-session bookkeeping (selectionToSessionId, openSelections,
// debugBuffers) is cleared by handleTerminalExit as the Go-side
// Close() fires terminalExitEvent for each session — the thunk
// only handles env-scoped state that the per-session exit chain
// would not unwind.
export const closeEnvironment =
  (selection: UISelection): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const tenant = selection.tenant.trim();
    const environment = selection.environment.trim();
    if (!tenant || !environment) {
      return;
    }
    try {
      await CloseEnvironmentSessions({ tenant, environment });
    } catch (error: unknown) {
      dispatch(showTerminalMessage(readError(error)));
      return;
    }
    // Both debug-mode variants of the selection produce distinct keys
    // in tabsByEnv / selectedSessionByEnv. Clear both so a follow-up
    // open in either mode starts from a blank slate.
    for (const debug of [false, true]) {
      const key = selectionKey({ tenant, environment, debug: debug || undefined });
      dispatch(clearTabsForEnv(key));
      dispatch(clearSelectedSessionForEnv(key));
    }
    dispatch(clearEnvOpening({ tenant, environment }));
    const current = getState().selection.selected;
    if (current?.tenant === tenant && current.environment === environment) {
      dispatch(setSelected(null));
      dispatch(setSessionId(0));
      dispatch(setDebugOutput(''));
    }
  };
