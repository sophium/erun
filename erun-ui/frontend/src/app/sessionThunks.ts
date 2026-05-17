import type { StartSessionResult, UISelection } from '@/types';

import {
  CloseSession,
  StartAISession,
  StartDeploySession,
  StartInitSession,
  StartLocalSession,
  StartSession,
} from '../../wailsjs/go/main/App';
import { sessionApi } from './api/sessionApi';
import {
  appendDebugOutput,
  applyPendingDebugHeader,
  setPendingDebugHeader,
  syncDebugDisplay,
} from './debugThunks';
import { readError } from './errors';
import type { IDEKind } from './model';
import {
  dismissNotification,
  dismissTerminalStatus,
  hideTerminalMessage,
  showNotification,
  showTerminalFailure,
  showTerminalMessage,
} from './notificationThunks';
import { loadReviewDiff } from './reviewThunks';
import { selectActiveSlotForSelection, selectEnvironmentExists } from './selectors';
import { isNewSessionSelection } from './sessionSelection';
import { setIdleStatus } from './slices/idleSlice';
import {
  setSelectedDiffPath,
  setSelectedReviewCommit,
  setSelectedReviewScope,
} from './slices/reviewSlice';
import { setSelected } from './slices/selectionSlice';
import {
  clearSessionDebug,
  registerDebugSession,
  setSessionDebug,
  trackOpenSession,
} from './slices/sessionsSlice';
import { setTenantDashboard } from './slices/tenantDashboardSlice';
import { setDebugOutput, setSelectedSessionForEnv, setSessionId } from './slices/terminalSlice';
import { setTerminalCopyOutput, setTerminalCopyStatus } from './slices/terminalStatusSlice';
import type { TerminalTabKind } from './state';
import type { AppThunk } from './store';
import { recordTab, rememberSelectedTab, removeTab } from './tabsThunks';
import {
  debugOutputBlock,
  formatDebugCommand,
  formatIDECommand,
  ideLabel,
  ideOpenFailure,
} from './terminalStatus';
import { requireController } from './thunkExtra';
import { selectionKey } from './versionSuggestions';

// sessionThunks own every state-side interaction with terminal sessions:
// opening an env, starting init/deploy, swapping tabs in the strip,
// closing tabs, opening an IDE, and the helper machinery (spawn passive
// tabs, restore remembered tab, show "Opening …" status). The controller
// owns only the imperative xterm calls (fit, focus, queue resize); these
// thunks call into it via `extra.controller`.

// spawnDefaultTab tries to start a Local or AI tab in the background.
// Failures are swallowed: the tool may not be installed on this machine,
// and a later open will retry. Recording the tab requires the start call
// to succeed.
const spawnDefaultTab =
  (
    key: string,
    runSelection: UISelection,
    kind: 'local' | 'ai',
    label: string,
    cols: number,
    rows: number,
  ): AppThunk<Promise<void>> =>
  async (dispatch) => {
    const start = kind === 'local' ? StartLocalSession : StartAISession;
    try {
      const result = (await start(runSelection, 0, cols, rows)) as StartSessionResult;
      dispatch(recordTab(key, result.sessionId, result.slot ?? 0, kind, label));
    } catch {
      // Tool unavailable; future env opens will retry.
    }
  };

// spawnERunTabPassive starts an ERun tab without flipping the active
// session. The shared open-tracking + debug-session bookkeeping happens
// inline because registerOpenSessionResult flips the active session and
// would override the user's current view.
const spawnERunTabPassive =
  (key: string, runSelection: UISelection, cols: number, rows: number): AppThunk<Promise<void>> =>
  async (dispatch) => {
    try {
      const result = (await StartSession(runSelection, 0, cols, rows)) as StartSessionResult;
      dispatch(trackOpenSession({ key, sessionId: result.sessionId, selection: runSelection }));
      dispatch(
        registerDebugSession({
          sessionId: result.sessionId,
          selection: runSelection,
          mode: 'open',
        }),
      );
      dispatch(recordTab(key, result.sessionId, result.slot ?? 0, 'erun', 'ERun'));
    } catch {
      // ERun failed to spawn; future env opens will retry.
    }
  };

const ensureDefaultEnvTabs =
  (runSelection: UISelection, key: string, cols: number, rows: number): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const tabs = getState().terminal.tabsByEnv[key] ?? [];
    if (!tabs.some((tab) => tab.kind === 'erun')) {
      await dispatch(spawnERunTabPassive(key, runSelection, cols, rows));
    }
    if (!tabs.some((tab) => tab.kind === 'local')) {
      await dispatch(spawnDefaultTab(key, runSelection, 'local', 'Local', cols, rows));
    }
    if (!tabs.some((tab) => tab.kind === 'ai')) {
      await dispatch(spawnDefaultTab(key, runSelection, 'ai', 'AI', cols, rows));
    }
  };

export const registerOpenSessionResult =
  (key: string, result: StartSessionResult, runSelection: UISelection): AppThunk =>
  (dispatch, getState) => {
    dispatch(trackOpenSession({ key, sessionId: result.sessionId, selection: runSelection }));
    dispatch(
      registerDebugSession({ sessionId: result.sessionId, selection: runSelection, mode: 'open' }),
    );
    dispatch(applyPendingDebugHeader(result.sessionId));
    dispatch(setSessionId(result.sessionId));
    // Preserve the user's prior tab choice for this env across re-opens
    // (Nielsen heuristic #4: consistency / user control). Only seed
    // selectedSessionByEnv when nothing is remembered, or when the
    // remembered session no longer exists in the live tabs for this env.
    // restoreSelectedTabForEnv below switches the terminal back to the
    // remembered tab when one exists.
    const state = getState();
    const remembered = state.terminal.selectedSessionByEnv[key];
    const liveTabs = state.terminal.tabsByEnv[key] ?? [];
    const rememberedIsLive = remembered && liveTabs.some((tab) => tab.sessionId === remembered);
    if (!rememberedIsLive) {
      dispatch(setSelectedSessionForEnv({ key, sessionId: result.sessionId }));
    }
    dispatch(syncDebugDisplay());
    const slot = result.slot ?? 0;
    const kind: TerminalTabKind = slot === 0 ? 'erun' : 'extra';
    const label = kind === 'erun' ? 'ERun' : `Terminal ${String(slot)}`;
    dispatch(recordTab(key, result.sessionId, slot, kind, label));
  };

const prepareOpenSelection =
  (
    selection: UISelection,
    runSelection: UISelection,
    previousSessionId: number,
    previousKnownSessionId: number,
  ): AppThunk =>
  (dispatch, getState) => {
    const state = getState();
    if (
      selectionKey(selection) !==
      selectionKey(state.selection.selected ?? { tenant: '', environment: '' })
    ) {
      dispatch(setSelectedReviewScope('current'));
      dispatch(setSelectedReviewCommit(''));
      dispatch(setSelectedDiffPath(''));
    }
    dispatch(setSelected(selection));
    dispatch(setIdleStatus(null));
    if (!isNewSessionSelection(previousSessionId, previousKnownSessionId)) {
      return;
    }
    if (state.layout.debugOpen) {
      dispatch(setPendingDebugHeader(`$ ${formatDebugCommand(runSelection)}\n`));
    }
    dispatch(setTerminalCopyOutput(''));
    dispatch(setTerminalCopyStatus(''));
    dispatch(
      showTerminalMessage(`Opening ${selection.tenant} / ${selection.environment}...`, true),
    );
  };

const restoreSelectedTabForEnv =
  (key: string): AppThunk =>
  (dispatch, getState) => {
    const state = getState();
    const tabs = state.terminal.tabsByEnv[key] ?? [];
    const remembered = state.terminal.selectedSessionByEnv[key];
    if (!remembered || !tabs.some((tab) => tab.sessionId === remembered)) {
      return;
    }
    if (remembered === state.terminal.sessionId) {
      return;
    }
    dispatch(selectTerminalTab(remembered));
  };

const showOpenSelectionStatus =
  (sessionId: number, selection: UISelection): AppThunk =>
  (dispatch, getState, extra) => {
    const controller = requireController(extra);
    const state = getState();
    const exitReason = state.sessions.exitReasons[sessionId] ?? '';
    if (exitReason) {
      dispatch(setTerminalCopyOutput(state.sessions.exitOutputs[sessionId] ?? ''));
      dispatch(setTerminalCopyStatus(''));
      dispatch(showTerminalMessage(exitReason));
      return;
    }
    if (controller.sessions.displayBuffer(sessionId).length > 0) {
      dispatch(hideTerminalMessage());
      return;
    }
    dispatch(
      showTerminalMessage(`Opening ${selection.tenant} / ${selection.environment}...`, true),
    );
  };

export const openSelection =
  (selection: UISelection): AppThunk<Promise<void>> =>
  async (dispatch, getState, extra) => {
    const controller = requireController(extra);
    dispatch(
      setTenantDashboard({ tenant: '', tab: 'users', loading: false, error: '', data: null }),
    );
    const debugOpen = getState().layout.debugOpen;
    const runSelection = { ...selection, debug: debugOpen || undefined };
    const key = selectionKey(runSelection);
    const previousSessionId = getState().terminal.sessionId;
    const previousKnownSessionId = getState().sessions.selectionToSessionId[key] ?? 0;
    // Capture the previous sidebar selection so a failed StartSession can
    // roll it back. prepareOpenSelection has already dispatched setSelected,
    // so the sidebar visually moved to the new env; without rollback the
    // sidebar would point at one env while the terminal still shows the
    // previous one.
    const previousSelected = getState().selection.selected;

    dispatch(
      prepareOpenSelection(selection, runSelection, previousSessionId, previousKnownSessionId),
    );
    controller.fitTerminal();
    const { cols, rows } = controller.terminalSize();

    try {
      // Spawn Local first so subsequent ERun/AI spawns can log into it.
      const tabs = getState().terminal.tabsByEnv[key] ?? [];
      if (!tabs.some((tab) => tab.kind === 'local')) {
        await dispatch(spawnDefaultTab(key, runSelection, 'local', 'Local', cols, rows));
      }

      const slot = selectActiveSlotForSelection(getState(), runSelection);
      const result = (await StartSession(runSelection, slot, cols, rows)) as StartSessionResult;
      dispatch(registerOpenSessionResult(key, result, runSelection));
      dispatch(showOpenSelectionStatus(result.sessionId, selection));

      await dispatch(ensureDefaultEnvTabs(runSelection, key, cols, rows));
      dispatch(restoreSelectedTabForEnv(key));

      if (getState().layout.reviewOpen) {
        await dispatch(loadReviewDiff());
      }
      controller.focusTerminalSoon();
      controller.queueTerminalResize();
    } catch (error: unknown) {
      dispatch(setSelected(previousSelected));
      dispatch(showTerminalMessage(readError(error)));
      throw error;
    }
  };

// activateLocalAfterCommand promotes the freshly-spawned Local session as
// the active tab once an init/deploy command finishes. Init does not
// spawn dependent tabs because the env config does not yet exist; the
// environment-initialized handler reopens the env once the backend
// confirms creation.
export const activateLocalAfterCommand =
  (selection: UISelection, result: StartSessionResult): AppThunk<Promise<void>> =>
  async (dispatch, getState, extra) => {
    const controller = requireController(extra);
    const debugOpen = getState().layout.debugOpen;
    const runSelection = { ...selection, debug: debugOpen || undefined };
    const key = selectionKey(runSelection);
    dispatch(recordTab(key, result.sessionId, result.slot ?? 0, 'local', 'Local'));
    if (selectEnvironmentExists(getState(), selection.tenant, selection.environment)) {
      const { cols, rows } = controller.terminalSize();
      await dispatch(ensureDefaultEnvTabs(runSelection, key, cols, rows));
    }
    dispatch(setSessionId(result.sessionId));
    dispatch(setSelectedSessionForEnv({ key, sessionId: result.sessionId }));
    dispatch(hideTerminalMessage());
    controller.focusTerminalSoon();
    controller.queueTerminalResize();
  };

// Visibility of system status (Nielsen #1) for `erun init` is provided by
// three persistent surfaces: the sidebar placeholder row, the activity
// drawer init entry, and the live `erun init` output in the Local tab.
// Setting state.terminalMessage would flash a busy overlay for ~150 ms
// inside the still-open modal and then be cleared by
// activateLocalAfterCommand before the user could register it — see
// erun-ui/AGENTS.md § "UX Impact Review Checklist" item 3.
export const startInitSelection =
  (selection: UISelection): AppThunk<Promise<void>> =>
  async (dispatch, getState, extra) => {
    const controller = requireController(extra);
    const debugOpen = getState().layout.debugOpen;
    const runSelection = { ...selection, debug: debugOpen || undefined };
    dispatch(setSelected(selection));
    dispatch(setTerminalCopyOutput(''));
    dispatch(setTerminalCopyStatus(''));
    controller.fitTerminal();
    const { cols, rows } = controller.terminalSize();
    const result = (await StartInitSession(runSelection, cols, rows)) as StartSessionResult;
    await dispatch(activateLocalAfterCommand(selection, result));
  };

export const startDeploySelection =
  (selection: UISelection): AppThunk<Promise<void>> =>
  async (dispatch, getState, extra) => {
    const controller = requireController(extra);
    const debugOpen = getState().layout.debugOpen;
    const runSelection = { ...selection, debug: debugOpen || undefined };
    dispatch(setSelected(selection));
    dispatch(setTerminalCopyOutput(''));
    dispatch(setTerminalCopyStatus(''));
    dispatch(
      showTerminalMessage(
        `Deploying runtime for ${selection.tenant} / ${selection.environment}...`,
        true,
      ),
    );
    controller.fitTerminal();
    const { cols, rows } = controller.terminalSize();
    const result = (await StartDeploySession(runSelection, cols, rows)) as StartSessionResult;
    await dispatch(activateLocalAfterCommand(selection, result));
  };

export const addTerminalTab = (): AppThunk<Promise<void>> => async (dispatch, getState, extra) => {
  const controller = requireController(extra);
  const state = getState();
  const selection = state.selection.selected;
  if (!selection) {
    return;
  }
  const runSelection = { ...selection, debug: state.layout.debugOpen || undefined };
  const key = selectionKey(runSelection);
  const tabs = state.terminal.tabsByEnv[key] ?? [];
  const nextSlot = tabs.reduce((max, tab) => (tab.slot >= max ? tab.slot + 1 : max), 0);
  try {
    const size = controller.terminalSize();
    const result = (await StartSession(
      runSelection,
      nextSlot,
      size.cols,
      size.rows,
    )) as StartSessionResult;
    dispatch(registerOpenSessionResult(key, result, runSelection));
    controller.focusTerminalSoon();
    controller.queueTerminalResize();
  } catch (error: unknown) {
    dispatch(showTerminalMessage(readError(error)));
  }
};

export const selectTerminalTab =
  (sessionId: number): AppThunk =>
  (dispatch, getState, extra) => {
    const controller = requireController(extra);
    if (sessionId <= 0 || sessionId === getState().terminal.sessionId) {
      return;
    }
    dispatch(setSessionId(sessionId));
    dispatch(rememberSelectedTab(sessionId));
    dispatch(syncDebugDisplay());
    const state = getState();
    const exitReason = state.sessions.exitReasons[sessionId] ?? '';
    if (exitReason) {
      dispatch(setTerminalCopyOutput(state.sessions.exitOutputs[sessionId] ?? ''));
      dispatch(setTerminalCopyStatus(''));
      dispatch(showTerminalMessage(exitReason));
    } else {
      dispatch(hideTerminalMessage());
    }
    controller.focusTerminalSoon();
    controller.queueTerminalResize();
  };

export const closeTerminalTab =
  (sessionId: number): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    if (sessionId <= 0) {
      return;
    }
    const state = getState();
    const selection = state.selection.selected;
    if (!selection) {
      return;
    }
    const runSelection = { ...selection, debug: state.layout.debugOpen || undefined };
    const key = selectionKey(runSelection);
    const tabs = state.terminal.tabsByEnv[key] ?? [];
    const target = tabs.find((tab) => tab.sessionId === sessionId);
    if (target && target.kind !== 'extra') {
      return;
    }
    try {
      await CloseSession(sessionId);
    } catch (error: unknown) {
      dispatch(showTerminalMessage(readError(error)));
      return;
    }
    const remaining = dispatch(removeTab(key, sessionId));
    dispatch(clearSessionDebug(sessionId));
    if (getState().terminal.sessionId === sessionId) {
      const next = remaining[remaining.length - 1];
      if (next) {
        dispatch(selectTerminalTab(next.sessionId));
      } else {
        dispatch(setSessionId(0));
        dispatch(setDebugOutput(''));
      }
    }
  };

export const openIDE =
  (selection: UISelection | null, ide: IDEKind): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    if (!selection) {
      dispatch(showTerminalMessage('Choose an environment from the left pane.'));
      return;
    }
    const state = getState();
    const runSelection = { ...selection, debug: state.layout.debugOpen || undefined };
    const label = ideLabel(ide);
    dispatch(setSelected(selection));
    if (state.layout.debugOpen) {
      const header = `$ ${formatIDECommand(runSelection, ide)}\n`;
      dispatch(setSessionDebug({ sessionId: getState().terminal.sessionId, value: header }));
      dispatch(syncDebugDisplay());
    }
    dispatch(setTerminalCopyOutput(''));
    dispatch(setTerminalCopyStatus(''));
    dispatch(
      showTerminalMessage(`Opening ${label} for ${selection.tenant} / ${selection.environment}...`),
    );

    try {
      await dispatch(
        sessionApi.endpoints.openIDE.initiate({ selection: runSelection, ide }),
      ).unwrap();
    } catch (error: unknown) {
      const failure = ideOpenFailure(selection, label, readError(error));
      dispatch(appendDebugOutput(debugOutputBlock(failure.copyOutput)));
      dispatch(dismissNotification());
      dispatch(showTerminalFailure(failure.message, failure.detail, failure.copyOutput, '', null));
      return;
    }
    dispatch(dismissTerminalStatus());
    dispatch(
      showNotification(
        'success',
        `Opened ${label} for ${selection.tenant} / ${selection.environment}.`,
      ),
    );
  };
