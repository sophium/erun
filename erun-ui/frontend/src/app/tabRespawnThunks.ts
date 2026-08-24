import type { StartSessionResult, UISelection } from '@/types';

import {
  EndAISessions,
  StartAISession,
  StartContributeAISession,
  StartLocalSession,
  StartSession,
} from '../../wailsjs/go/main/App';
import { startAITabOrPrompt } from './aiOccupancyThunks';
import { readError } from './errors';
import { hideTerminalMessage, showTerminalMessage } from './notificationThunks';
import { selectEnvHasFailedDeploy } from './selectors';
import { trackOpenSession } from './slices/sessionsSlice';
import { setSelectedSessionForEnv, setSessionId } from './slices/terminalSlice';
import { setTerminalCopyOutput, setTerminalCopyStatus } from './slices/terminalStatusSlice';
import type { TerminalTab, TerminalTabKind } from './state';
import type { AppThunk } from './store';
import { recordTab } from './tabsThunks';
import { requireController } from './thunkExtra';
import { selectionKey } from './versionSuggestions';

// Click-driven recovery for the default terminal tabs (ai, local, erun):
// a tab can point at a dead PTY (e.g. the cloud context auto-stopped
// under an attached AI session), and clicking it should spawn a fresh
// session rather than re-show the stale "exit status N" output.

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
    const runSelection = { ...selection };
    const key = selectionKey(runSelection);
    const tab = state.terminal.tabsByEnv[key]?.find((t) => t.sessionId === sessionId);
    if (!tab || !respawnableDefaultKinds.has(tab.kind)) {
      return false;
    }
    // Refuse respawn when the env's deploy failed: reopening re-runs `erun open`,
    // which would re-deploy the broken env — the same re-deploy storm
    // auto-reconnect exists to stop. Recovery is left to the failed-deploy card.
    if (selectEnvHasFailedDeploy(state, selection.tenant, selection.environment)) {
      return false;
    }
    if (sessionId !== state.terminal.sessionId) {
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
    let result: StartSessionResult | null;
    try {
      if (tab.kind === 'ai') {
        result = await dispatch(
          startAITabOrPrompt({
            key,
            selection: runSelection,
            slot: tab.slot,
            cols,
            rows,
            label: tab.label,
          }),
        );
      } else {
        result = await startSessionForKind(runSelection, tab, cols, rows);
      }
    } catch (error: unknown) {
      dispatch(showTerminalMessage(readError(error)));
      return;
    }
    if (!result) {
      // The occupancy prompt took over; nothing to select until it resolves.
      dispatch(hideTerminalMessage());
      return;
    }
    if (tab.kind === 'erun') {
      dispatch(trackOpenSession({ key, sessionId: result.sessionId, selection: runSelection }));
    }
    dispatch(recordTab(key, result.sessionId, result.slot ?? tab.slot, tab.kind, tab.label));
    dispatch(setSelectedSessionForEnv({ key, sessionId: result.sessionId }));
    dispatch(setSessionId(result.sessionId));
    dispatch(hideTerminalMessage());
    controller.focusTerminalSoon();
    controller.queueTerminalResize();
  };

// startSessionForKind now only serves callers that never gate on occupancy: a
// dead AI tab click routes through startAITabOrPrompt above instead, but a
// launch-flag relaunch (respawnRelaunchedAITab) restarts a tool the user was
// already running, not a second agent, so it confirms straight through.
async function startSessionForKind(
  runSelection: UISelection,
  tab: TerminalTab,
  cols: number,
  rows: number,
): Promise<StartSessionResult> {
  switch (tab.kind) {
    case 'ai':
      return await StartAISession(runSelection, tab.slot, cols, rows, true);
    case 'local':
      return await StartLocalSession(runSelection, tab.slot, cols, rows);
    case 'erun':
      return await StartSession(runSelection, tab.slot, cols, rows);
    case 'contribute-ai':
      return await StartContributeAISession(runSelection, tab.slot, cols, rows);
    default:
      throw new Error(`cannot respawn tab kind ${tab.kind}`);
  }
}

function isAITabKind(tab: TerminalTab): boolean {
  return tab.kind === 'ai' || tab.kind === 'contribute-ai';
}

// relaunchAISessionsForLaunchChange applies a saved Claude launch-flag change
// to the env's AI sessions. Launch flags are read only when a pod session is
// created — reattaches never re-run the launch command — so applying them
// means ending the AI sessions and respawning them. The backend returns false
// when nothing needs reopening (a non-claude tool that launches verbatim).
export const relaunchAISessionsForLaunchChange =
  (selection: UISelection): AppThunk<Promise<void>> =>
  async (dispatch, getState, extra) => {
    const controller = requireController(extra);
    const state = getState();
    const runSelection = { ...selection };
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
    }
  };
