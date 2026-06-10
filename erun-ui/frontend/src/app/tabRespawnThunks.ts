import type { StartSessionResult, UISelection } from '@/types';

import {
  EndAISessions,
  StartAISession,
  StartContributeAISession,
  StartLocalSession,
  StartSession,
} from '../../wailsjs/go/main/App';
import { syncDebugDisplay } from './debugThunks';
import { readError } from './errors';
import { hideTerminalMessage, showTerminalMessage } from './notificationThunks';
import { selectEnvHasFailedDeploy } from './selectors';
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
    // Refuse the respawn when the env's deploy failed: reopening would re-run
    // `erun open` and re-deploy the broken env, the same re-deploy storm #447
    // stops for auto-reconnect. Returning false lets selectTerminalTab show the
    // dead session's captured failure output and the deploy-failed marker
    // instead, with recovery left to the failed-deploy card (Run doctor /
    // Rebuild & redeploy).
    if (selectEnvHasFailedDeploy(state, selection.tenant, selection.environment)) {
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
    case 'contribute-ai':
      return (await StartContributeAISession(
        runSelection,
        tab.slot,
        cols,
        rows,
      )) as StartSessionResult;
    default:
      throw new Error(`cannot respawn tab kind ${tab.kind}`);
  }
}

function isAITabKind(tab: TerminalTab): boolean {
  return tab.kind === 'ai' || tab.kind === 'contribute-ai';
}

// relaunchAISessionsForLaunchChange applies a saved Claude launch-flag change
// (--effort / --model / --verbose --debug, issues #477/#482) to the env's AI
// sessions. The launch command runs once when the persistent pod session is
// created — reattaches never re-run it — so the backend first ends the env's
// AI sessions (desktop and pod side); the AI tabs open in this window are
// then respawned, and the relaunched guard resumes the Claude conversation
// via --continue. Local/ERun tabs are untouched: their launch command does
// not change. With no open AI tab the end alone is enough — the next open
// creates a fresh session with the new flags. The backend reports false for
// envs whose AI tool launches verbatim (non-claude); nothing is reopened.
export const relaunchAISessionsForLaunchChange =
  (selection: UISelection): AppThunk<Promise<void>> =>
  async (dispatch, getState, extra) => {
    const controller = requireController(extra);
    const state = getState();
    const runSelection = { ...selection, debug: state.layout.debugOpen || undefined };
    const key = selectionKey(runSelection);
    const aiTabs = (state.terminal.tabsByEnv[key] ?? []).filter(isAITabKind);
    const hasTabs = aiTabs.length > 0;
    if (hasTabs) {
      dispatch(
        showTerminalMessage(
          `Reopening AI session for ${selection.tenant} / ${selection.environment}...`,
          true,
        ),
      );
    }
    try {
      const ended = await EndAISessions(runSelection);
      if (ended) {
        for (const tab of aiTabs) {
          await dispatch(respawnRelaunchedAITab(runSelection, key, tab, controller.terminalSize()));
        }
      }
      if (hasTabs) {
        dispatch(hideTerminalMessage());
        controller.queueTerminalResize();
      }
    } catch (error: unknown) {
      dispatch(showTerminalMessage(readError(error)));
    }
  };

// respawnRelaunchedAITab spawns the fresh AI session for one reopened tab and
// re-points the tab strip — and, when the dying session was the one on
// screen, the active terminal — at the new session id.
const respawnRelaunchedAITab =
  (
    runSelection: UISelection,
    key: string,
    tab: TerminalTab,
    size: { cols: number; rows: number },
  ): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const result = await startSessionForKind(runSelection, tab, size.cols, size.rows);
    const wasDisplayed = getState().terminal.sessionId === tab.sessionId;
    const wasSelectedForEnv = getState().terminal.selectedSessionByEnv[key] === tab.sessionId;
    dispatch(recordTab(key, result.sessionId, result.slot ?? tab.slot, tab.kind, tab.label));
    if (wasSelectedForEnv) {
      dispatch(setSelectedSessionForEnv({ key, sessionId: result.sessionId }));
    }
    if (wasDisplayed) {
      dispatch(setSessionId(result.sessionId));
      dispatch(syncDebugDisplay());
    }
  };
