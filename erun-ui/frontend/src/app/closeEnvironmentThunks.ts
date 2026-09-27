import type { UISelection } from '@/types';

import { CloseEnvironmentSessions } from '../../wailsjs/go/main/App';
import { readError } from './errors';
import { showTerminalError } from './notificationThunks';
import { setAIBusyForEnv } from './slices/aiActivitySlice';
import { setSelected } from './slices/selectionSlice';
import {
  adoptClosingSessions,
  clearEnvClosing,
  clearEnvOpening,
  markEnvClosing,
  releaseClosingSessions,
} from './slices/sessionsSlice';
import { clearSelectedSessionForEnv, clearTabsForEnv, setSessionId } from './slices/terminalSlice';
import type { AppThunk } from './store';
import { envSessionIds } from './tabsThunks';
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
    const key = selectionKey({ tenant, environment });
    // Marked before the RPC even starts, so it is in place before any of this
    // close's own terminal-exit events can possibly arrive. Those events land
    // asynchronously over the SSE stream and race clearTabsForEnv below — an
    // ERun-tab exit processed first (while the AI tab is still nominally
    // present) would otherwise read the tear-down as an unexpected death and
    // respawn a sibling default tab moments after the user closed it.
    dispatch(markEnvClosing(key));
    // The sessions this close is about to tear down, named before it starts.
    // They have to be recognisable as this close's even after it settles,
    // because a close can kill and still come back an error.
    dispatch(adoptClosingSessions({ key, sessionIds: envSessionIds(getState(), key) }));
    try {
      await CloseEnvironmentSessions({ tenant, environment });
    } catch (error: unknown) {
      // The in-flight hold comes off here and the per-session marks stay on.
      // That asymmetry is the point: the error itself is not evidence that
      // nothing was torn down — the desktop walks the env's sessions and
      // continues past one whose Close failed, so every session it had already
      // reached is dead and its exit is still on its way here — but it is
      // evidence that the desktop is done walking. So the env stops being held
      // wholesale (a session this close never touched can report again), and
      // the exits it did cause are answered one session at a time by the marks.
      dispatch(clearEnvClosing(key));
      dispatch(showTerminalError(readError(error)));
      return;
    }
    dispatch(clearTabsForEnv(key));
    // A close that returned cleanly is done, so nothing stays marked: the
    // desktop killed everything it was given and the tabs go with it just
    // below, which is the release this path has always done. The marks are for
    // the branch above, where the RPC cannot say what it got through.
    dispatch(releaseClosingSessions(key));
    dispatch(clearEnvClosing(key));
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
