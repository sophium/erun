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
import { resolveAutoStartGate } from './autoStartGate';
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
import { setAutoStartPrompt } from './slices/autoStartPromptSlice';
import { setIdleStatus } from './slices/idleSlice';
import {
  setSelectedDiffPath,
  setSelectedReviewCommit,
  setSelectedReviewScope,
} from './slices/reviewSlice';
import { setSelected } from './slices/selectionSlice';
import {
  clearEnvOpening,
  clearSessionDebug,
  markEnvOpening,
  registerDebugSession,
  resetEnvOpening,
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

// surfaceEnvSession repoints the visible terminal at the new env's
// preferred session: the remembered tab if it still exists, else a
// Local tab for the env, else 0. The terminal renderer drops PTY
// writes that do not match the current sessionId, so this prevents
// the previous env's content from continuing to paint while the new
// env's slower StartSession is in flight.
const surfaceEnvSession =
  (key: string): AppThunk =>
  (dispatch, getState) => {
    const state = getState();
    const tabs = state.terminal.tabsByEnv[key] ?? [];
    const remembered = state.terminal.selectedSessionByEnv[key] ?? 0;
    const next = tabs.some((tab) => tab.sessionId === remembered)
      ? remembered
      : (tabs.find((tab) => tab.kind === 'local')?.sessionId ?? 0);
    dispatch(setSessionId(next));
  };

// trackOpenSessionMetadata records the session bookkeeping that should
// fire whether or not the user is still on this env: the session
// existence (so a later sidebar click can reuse instead of double-spawning)
// and the tab entry. registerOpenSessionResult composes this with the
// "promote to current view" half; the stale-selection path in openSelection
// calls only this helper so an abandoned long-running open does not steal
// the terminal away from the env the user has navigated to.
const trackOpenSessionMetadata =
  (key: string, result: StartSessionResult, runSelection: UISelection): AppThunk =>
  (dispatch) => {
    dispatch(trackOpenSession({ key, sessionId: result.sessionId, selection: runSelection }));
    dispatch(
      registerDebugSession({ sessionId: result.sessionId, selection: runSelection, mode: 'open' }),
    );
    const slot = result.slot ?? 0;
    const kind: TerminalTabKind = slot === 0 ? 'erun' : 'extra';
    const label = kind === 'erun' ? 'ERun' : `Terminal ${String(slot)}`;
    dispatch(recordTab(key, result.sessionId, slot, kind, label));
  };

export const registerOpenSessionResult =
  (key: string, result: StartSessionResult, runSelection: UISelection): AppThunk =>
  (dispatch, getState) => {
    dispatch(trackOpenSessionMetadata(key, result, runSelection));
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
    const previousKey = state.selection.selected ? selectionKey(state.selection.selected) : '';
    const newKey = selectionKey(selection);
    if (newKey !== previousKey) {
      dispatch(setSelectedReviewScope('current'));
      dispatch(setSelectedReviewCommit(''));
      dispatch(setSelectedDiffPath(''));
    }
    // setSelected is observed by selectionSyncMiddleware, which reconciles
    // state.terminal.sessionId with the new env's tabs in the same tick.
    // Every setSelected dispatch — including re-clicks where newKey ===
    // previousKey but the terminal happens to point at a stale session
    // from another env — flows through the listener, so no caller has to
    // remember to pair setSelected with a manual surface.
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

    const verdict = await resolveAutoStartGate(selection, getState);
    if (verdict === 'prompt') {
      dispatch(
        setAutoStartPrompt({
          open: true,
          selection: { ...selection },
          saving: false,
          error: '',
        }),
      );
      return;
    }
    const shouldSpawnERun = verdict !== 'skip-erun';

    const isCurrentSelection = createIsCurrentSelection(getState, selection);

    // Reset openingByEnv before the new selection paints its own spinner.
    // The previous click's openSelection is still in flight in the
    // background; isCurrentSelection keeps that flow from stomping on
    // the new selection's status banner or terminal id, and this reset
    // keeps the sidebar spinner from lingering on the env the user has
    // navigated away from.
    dispatch(resetEnvOpening());
    dispatch(
      prepareOpenSelection(selection, runSelection, previousSessionId, previousKnownSessionId),
    );
    controller.fitTerminal();
    const { cols, rows } = controller.terminalSize();

    dispatch(markEnvOpening({ tenant: selection.tenant, environment: selection.environment }));
    try {
      // Spawn Local first so subsequent ERun/AI spawns can log into it.
      const tabs = getState().terminal.tabsByEnv[key] ?? [];
      if (!tabs.some((tab) => tab.kind === 'local')) {
        await dispatch(spawnDefaultTab(key, runSelection, 'local', 'Local', cols, rows));
      }
      if (!isCurrentSelection()) {
        return;
      }
      // Now that Local is guaranteed to exist for this env, repoint the
      // visible terminal at it. prepareOpenSelection has already
      // cleared sessionId to 0 if the env changed; this fills it back
      // in with the just-spawned (or already-existing) Local so the
      // user sees their new env's terminal while ERun cold-starts.
      dispatch(surfaceEnvSession(key));

      if (!shouldSpawnERun) {
        // autoStart=never path: navigation completes with Local only. The
        // user can flip the policy from the manage-env dialog or click the
        // titlebar Play button to start the cloud context on demand, which
        // will surface "running" on the next idle poll and lets a follow-up
        // sidebar click spawn the ERun tab.
        dispatch(hideTerminalMessage());
        controller.focusTerminalSoon();
        controller.queueTerminalResize();
        return;
      }

      const slot = selectActiveSlotForSelection(getState(), runSelection);
      const result = (await StartSession(runSelection, slot, cols, rows)) as StartSessionResult;
      await dispatch(
        finishOpenSession(key, result, runSelection, selection, cols, rows, isCurrentSelection),
      );
    } catch (error: unknown) {
      if (isCurrentSelection()) {
        dispatch(setSelected(previousSelected));
        dispatch(showTerminalMessage(readError(error)));
      }
      throw error;
    } finally {
      dispatch(clearEnvOpening({ tenant: selection.tenant, environment: selection.environment }));
    }
  };

// createIsCurrentSelection captures the click's target selection and
// returns a predicate that any post-await dispatch can poll to decide
// whether to keep painting for `selection` or drop work because the user
// has navigated to a different env. The check reads getState() afresh
// each call so it tracks setSelected dispatches that fire between awaits.
function createIsCurrentSelection(
  getState: () => import('./store').RootState,
  selection: UISelection,
): () => boolean {
  return () => {
    const current = getState().selection.selected;
    if (current === null) {
      return false;
    }
    return current.tenant === selection.tenant && current.environment === selection.environment;
  };
}

// finishOpenSession owns the post-StartSession work: tab promotion, status
// banner, default-tab ensure, review diff refresh. Splitting it out keeps
// openSelection's branching budget under the linter's ceiling, and gives
// the stale-selection bail-outs a single home. When the user has navigated
// away (isCurrentSelection() === false), the spawned session is recorded
// for later reuse but not promoted to the visible terminal — so a long-
// running cold EC2 open in env A no longer paints "Opening A..." over env
// B that the user has since clicked into.
const finishOpenSession =
  (
    key: string,
    result: StartSessionResult,
    runSelection: UISelection,
    selection: UISelection,
    cols: number,
    rows: number,
    isCurrentSelection: () => boolean,
  ): AppThunk<Promise<void>> =>
  async (dispatch, getState, extra) => {
    const controller = requireController(extra);
    if (!isCurrentSelection()) {
      dispatch(trackOpenSessionMetadata(key, result, runSelection));
      return;
    }
    dispatch(registerOpenSessionResult(key, result, runSelection));
    dispatch(showOpenSelectionStatus(result.sessionId, selection));

    await dispatch(ensureDefaultEnvTabs(runSelection, key, cols, rows));
    if (!isCurrentSelection()) {
      return;
    }
    dispatch(restoreSelectedTabForEnv(key));

    if (getState().layout.reviewOpen) {
      await dispatch(loadReviewDiff());
    }
    if (!isCurrentSelection()) {
      return;
    }
    controller.focusTerminalSoon();
    controller.queueTerminalResize();
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
