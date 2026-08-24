import type { UISelection } from '@/types';

import { CloseEnvironmentSessions } from '../../wailsjs/go/main/App';
import { readError } from './errors';
import { showTerminalError } from './notificationThunks';
import { setAIBusyForEnv } from './slices/aiActivitySlice';
import { setSelected } from './slices/selectionSlice';
import { clearEnvOpening } from './slices/sessionsSlice';
import { clearSelectedSessionForEnv, clearTabsForEnv, setSessionId } from './slices/terminalSlice';
import type { AppThunk } from './store';
import { selectionKey } from './versionSuggestions';

// closeEnvironment tears down an env's desktop tabs and session
// state (the sidebar's "open env" dot). Desktop-only — it does NOT
// touch cloud state, unlike the cloud-context Stop button.
// Per-session bookkeeping is unwound separately by handleTerminalExit
// as each closed session fires its exit event; this thunk owns only
// the env-scoped state that chain leaves behind.
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
      dispatch(showTerminalError(readError(error)));
      return;
    }
    const key = selectionKey({ tenant, environment });
    dispatch(clearTabsForEnv(key));
    // Close is a definitive teardown of the desktop view; clear the AI-busy
    // latch so the sidebar row stops spinning even if the backend's busy=false
    // event is delayed or missed. The pod AI session keeps repainting after
    // close, so recordAIActivity's idle clear may never fire on its own.
    dispatch(setAIBusyForEnv({ key, busy: false }));
    dispatch(clearSelectedSessionForEnv(key));
    dispatch(clearEnvOpening({ tenant, environment }));
    const current = getState().selection.selected;
    if (current?.tenant === tenant && current.environment === environment) {
      dispatch(setSelected(null));
      dispatch(setSessionId(0));
    }
  };
