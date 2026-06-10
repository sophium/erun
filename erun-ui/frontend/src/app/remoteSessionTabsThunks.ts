import type { StartSessionResult, UISelection } from '@/types';

import { ListRemoteAppSessions, StartSession } from '../../wailsjs/go/main/App';
import { registerDebugSession, trackOpenSession } from './slices/sessionsSlice';
import type { AppThunk } from './store';
import { recordTab } from './tabsThunks';

// reattachRemoteTerminalTabs rebuilds tabs for persistent pod sessions this
// window does not know about — custom terminals (`open-N`) created in another
// ERun window or a previous run that are still running in the pod. The default
// tabs attach-or-create on their own in ensureDefaultEnvTabs; this fills in
// the extras, so every running remote session ends up attached here (the
// attach itself takes the session over from any other window). Detection is
// best-effort and must never stall the open flow.
export const reattachRemoteTerminalTabs =
  (runSelection: UISelection, key: string, cols: number, rows: number): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    let ids: string[];
    try {
      // The generated binding is typed string[], but a Go nil slice arrives
      // as null at runtime — widen before defaulting.
      const listed = (await ListRemoteAppSessions(runSelection)) as string[] | null;
      ids = listed ?? [];
    } catch {
      return; // env may be local-only, unreachable, or not deployed
    }
    for (const id of ids) {
      const match = /^open-([1-9]\d*)$/.exec(id);
      if (!match) {
        continue;
      }
      const slot = Number(match[1]);
      const tabs = getState().terminal.tabsByEnv[key] ?? [];
      if (tabs.some((tab) => tab.slot === slot && tab.kind === 'extra')) {
        continue;
      }
      try {
        const result = (await StartSession(runSelection, slot, cols, rows)) as StartSessionResult;
        // Same bookkeeping trackOpenSessionMetadata does for the + button:
        // session registry, debug registry, and the tab entry — safe even if
        // the user has navigated to another env meanwhile.
        dispatch(trackOpenSession({ key, sessionId: result.sessionId, selection: runSelection }));
        dispatch(
          registerDebugSession({
            sessionId: result.sessionId,
            selection: runSelection,
            mode: 'open',
          }),
        );
        dispatch(recordTab(key, result.sessionId, slot, 'extra', `Terminal ${String(slot)}`));
      } catch {
        // skip sessions that fail to attach; the next env open retries
      }
    }
  };
