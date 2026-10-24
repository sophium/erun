import type { UISelection } from '@/types';

import { CloseEnvironmentSessions } from '../../wailsjs/go/main/App';
import { readError } from './errors';
import { showTerminalError } from './notificationThunks';
import { setAIBusyForEnv } from './slices/aiActivitySlice';
import { setSelected } from './slices/selectionSlice';
import { clearEnvClosing, clearEnvOpening, markEnvClosing } from './slices/sessionsSlice';
import { clearSelectedSessionForEnv, clearTabsForEnv, setSessionId } from './slices/terminalSlice';
import type { AppThunk } from './store';
import { forgetSessionIfDone } from './tabsThunks';
import { selectionKey } from './versionSuggestions';

// closeEnvironment tears down an env's desktop tabs and state, never cloud state.
// handleTerminalExit unwinds per-session bookkeeping.
export const closeEnvironment =
  (selection: UISelection): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const tenant = selection.tenant.trim();
    const environment = selection.environment.trim();
    if (!tenant || !environment) {
      return;
    }
    const key = selectionKey({ tenant, environment });
    // Marked before the RPC so this close's exit events are not mistaken for
    // unexpected deaths that respawn a default tab.
    dispatch(markEnvClosing(key));
    try {
      await CloseEnvironmentSessions({ tenant, environment });
    } catch (error: unknown) {
      dispatch(clearEnvClosing(key));
      dispatch(showTerminalError(readError(error)));
      return;
    }
    const closedSessions = (getState().terminal.tabsByEnv[key] ?? []).map((tab) => tab.sessionId);
    dispatch(clearTabsForEnv(key));
    for (const sessionId of closedSessions) {
      dispatch(forgetSessionIfDone(sessionId));
    }
    dispatch(clearEnvClosing(key));
    // The pod AI session keeps repainting after close, so its idle clear may never fire.
    dispatch(setAIBusyForEnv({ key, busy: false }));
    dispatch(clearSelectedSessionForEnv(key));
    dispatch(clearEnvOpening({ tenant, environment }));
    const current = getState().selection.selected;
    if (current?.tenant === tenant && current.environment === environment) {
      dispatch(setSelected(null));
      dispatch(setSessionId(0));
    }
  };
