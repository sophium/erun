import type { StartSessionResult, UISelection } from '@/types';

import { StartAISession, StartLocalSession, StartSession } from '../../wailsjs/go/main/App';
import { syncDebugDisplay } from './debugThunks';
import { readError } from './errors';
import { hideTerminalMessage, showTerminalMessage } from './notificationThunks';
import { registerDebugSession, trackOpenSession } from './slices/sessionsSlice';
import { setSelectedSessionForEnv, setSessionId } from './slices/terminalSlice';
import { setTerminalCopyOutput, setTerminalCopyStatus } from './slices/terminalStatusSlice';
import type { TerminalTab, TerminalTabKind } from './state';
import type { AppThunk } from './store';
import { recordTab } from './tabsThunks';
import { requireController } from './thunkExtra';
import { selectionKey } from './versionSuggestions';

// tabRespawnThunks own the click-driven recovery flow for the
// default-spawned terminal tabs (ai, local, erun). When a session's PTY
// has already exited — typically because the linked cloud context
// auto-stopped while a Claude/codex AI session was attached and
// `tryReconnect` refused to fight the stop — the tab still sits in
// `tabsByEnv` pointing at a dead `sessionId`. Without this flow, clicking
// it would re-show the stale "exit status N" pill on top of the frozen
// TUI output. Here we instead spawn a fresh session and reuse the tab
// entry, letting `terminalDisplayMiddleware` reset xterm onto the empty
// buffer that arrives with the new sessionId.

const respawnableDefaultKinds: ReadonlySet<TerminalTabKind> = new Set(['ai', 'local', 'erun']);

// maybeRespawnDeadDefaultTab returns true when the click was handled as
// a respawn and the caller should skip the regular setSessionId path.
// The respawn itself runs asynchronously; the caller must not assume the
// new sessionId is live by the time this returns.
export const maybeRespawnDeadDefaultTab =
  (sessionId: number): AppThunk<boolean> =>
  (dispatch, getState, extra) => {
    const controller = requireController(extra);
    const state = getState();
    if (!state.sessions.exitReasons[sessionId]) {
      return false;
    }
    const selection = state.selection.selected;
    if (!selection) {
      return false;
    }
    const runSelection = { ...selection, debug: state.layout.debugOpen || undefined };
    const key = selectionKey(runSelection);
    const tab = state.terminal.tabsByEnv[key]?.find((t) => t.sessionId === sessionId);
    if (!tab || !respawnableDefaultKinds.has(tab.kind)) {
      return false;
    }
    if (sessionId !== state.terminal.sessionId) {
      // Highlight the clicked tab and let terminalDisplayMiddleware wipe
      // any prior tab's content from xterm. The stale display buffer
      // briefly replays under the busy overlay, then gets reset again
      // when the new sessionId lands.
      dispatch(setSessionId(sessionId));
    }
    dispatch(setTerminalCopyOutput(''));
    dispatch(setTerminalCopyStatus(''));
    dispatch(
      showTerminalMessage(
        `Reopening ${respawnLabelForKind(tab.kind)} for ${selection.tenant} / ${selection.environment}...`,
        true,
      ),
    );
    const { cols, rows } = controller.terminalSize();
    void dispatch(respawnDefaultTab(runSelection, tab, key, cols, rows));
    return true;
  };

function respawnLabelForKind(kind: TerminalTabKind): string {
  switch (kind) {
    case 'ai':
      return 'AI session';
    case 'local':
      return 'Local shell';
    case 'erun':
      return 'ERun session';
    default:
      return 'session';
  }
}

const respawnDefaultTab =
  (
    runSelection: UISelection,
    tab: TerminalTab,
    key: string,
    cols: number,
    rows: number,
  ): AppThunk<Promise<void>> =>
  async (dispatch, _getState, extra) => {
    const controller = requireController(extra);
    let result: StartSessionResult;
    try {
      result = await startSessionForKind(runSelection, tab, cols, rows);
    } catch (error: unknown) {
      dispatch(showTerminalMessage(readError(error)));
      return;
    }
    if (tab.kind === 'erun') {
      dispatch(trackOpenSession({ key, sessionId: result.sessionId, selection: runSelection }));
      dispatch(
        registerDebugSession({
          sessionId: result.sessionId,
          selection: runSelection,
          mode: 'open',
        }),
      );
    }
    dispatch(recordTab(key, result.sessionId, result.slot ?? tab.slot, tab.kind, tab.label));
    dispatch(setSelectedSessionForEnv({ key, sessionId: result.sessionId }));
    dispatch(setSessionId(result.sessionId));
    dispatch(syncDebugDisplay());
    dispatch(hideTerminalMessage());
    controller.focusTerminalSoon();
    controller.queueTerminalResize();
  };

async function startSessionForKind(
  runSelection: UISelection,
  tab: TerminalTab,
  cols: number,
  rows: number,
): Promise<StartSessionResult> {
  switch (tab.kind) {
    case 'ai':
      return (await StartAISession(runSelection, tab.slot, cols, rows)) as StartSessionResult;
    case 'local':
      return (await StartLocalSession(runSelection, tab.slot, cols, rows)) as StartSessionResult;
    case 'erun':
      return (await StartSession(runSelection, tab.slot, cols, rows)) as StartSessionResult;
    default:
      throw new Error(`cannot respawn tab kind ${tab.kind}`);
  }
}
