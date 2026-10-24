import {
  CreateOrchestrator,
  DeleteOrchestrator,
  InvestigateFailure,
  ListOrchestrators,
  ReportBugFailure,
  ResolveOrchestratorToReopen,
  RestartApp,
  RestartOrchestrator,
  RevealOrchestratorGuidance,
  StartOrchestrator,
  StopOrchestrator,
  UpdateOrchestrator,
} from '../../wailsjs/go/main/App';
import { BrowserOpenURL, ClipboardSetText } from '../../wailsjs/runtime/runtime';
import { stateApi } from './api/stateApi';
import { buildDiagnosticsIssueURL, diagnosticsIssueBody } from './diagnosticsIssue';
import { readError } from './errors';
import type { IDEKind, OrchestratorGuidanceLayer } from './model';
import { showNotification } from './notificationThunks';
import { planOrchestratorBusySeed } from './orchestratorBusySeed';
import { planOrchestratorRestore } from './orchestratorRestore';
import { planOrchestratorShellSeed } from './orchestratorShellActivitySeed';
import { setAIBusyForSession } from './slices/aiActivitySlice';
import { setShellActivityForSession } from './slices/orchestratorShellActivitySlice';
import {
  closeOrchestratorDialog,
  type OrchestratorAlias,
  type OrchestratorEnvRef,
  type OrchestratorInfo,
  type OrchestratorLaunchSettings,
  setOrchestrators,
  setOrchestratorsBusy,
  setOrchestratorsError,
} from './slices/orchestratorsSlice';
import { setSelected } from './slices/selectionSlice';
import { resetTenantDashboard } from './slices/tenantDashboardSlice';
import { setSessionId } from './slices/terminalSlice';
import type { AppThunk } from './store';

// focusOrchestratorSession clears the tenant dashboard and env selection, which
// would otherwise paint over the focused session.
const focusOrchestratorSession =
  (sessionId: number): AppThunk =>
  (dispatch) => {
    dispatch(resetTenantDashboard());
    dispatch(setSelected(null));
    dispatch(setSessionId(sessionId));
  };

// loadOrchestrators seeds AI-busy and shell activity from the list, so a fetch
// after a missed event still renders the true state.
export const loadOrchestrators = (): AppThunk<Promise<void>> => async (dispatch) => {
  try {
    const list = (await ListOrchestrators()) as OrchestratorInfo[] | null;
    const items = list ?? [];
    dispatch(setOrchestrators(items));
    for (const seed of planOrchestratorBusySeed(items)) {
      dispatch(setAIBusyForSession(seed));
    }
    for (const seed of planOrchestratorShellSeed(items)) {
      dispatch(
        setShellActivityForSession({
          sessionId: seed.sessionId,
          activity: {
            running: seed.running,
            command: seed.command,
            startedAtUnix: seed.startedAtUnix,
          },
        }),
      );
    }
  } catch (error) {
    dispatch(setOrchestratorsError(readError(error)));
  }
};

export const createOrchestrator =
  (
    name: string,
    envs: OrchestratorEnvRef[],
    directories: string[],
    alias: OrchestratorAlias,
    launch: OrchestratorLaunchSettings,
  ): AppThunk<Promise<void>> =>
  async (dispatch) => {
    dispatch(setOrchestratorsBusy(true));
    try {
      await CreateOrchestrator(name, envs, directories, alias, launch);
      dispatch(closeOrchestratorDialog());
      await dispatch(loadOrchestrators());
      dispatch(setOrchestratorsBusy(false));
    } catch (error) {
      dispatch(setOrchestratorsError(readError(error)));
    }
  };

// updateOrchestrator sends every field: the backend replaces the whole definition.
export const updateOrchestrator =
  (
    id: string,
    name: string,
    envs: OrchestratorEnvRef[],
    directories: string[],
    alias: OrchestratorAlias,
    launch: OrchestratorLaunchSettings,
  ): AppThunk<Promise<void>> =>
  async (dispatch) => {
    dispatch(setOrchestratorsBusy(true));
    try {
      await UpdateOrchestrator(id, name, envs, directories, alias, launch);
      dispatch(closeOrchestratorDialog());
      await dispatch(loadOrchestrators());
      dispatch(setOrchestratorsBusy(false));
    } catch (error) {
      dispatch(setOrchestratorsError(readError(error)));
    }
  };

export const startOrchestrator =
  (id: string): AppThunk<Promise<void>> =>
  async (dispatch) => {
    try {
      const info = await StartOrchestrator(id, 80, 24);
      dispatch(focusOrchestratorSession(info.sessionId));
      await dispatch(loadOrchestrators());
    } catch (error) {
      dispatch(setOrchestratorsError(readError(error)));
    }
  };

export const openOrchestrator =
  (sessionId: number): AppThunk =>
  (dispatch) => {
    if (sessionId > 0) {
      dispatch(focusOrchestratorSession(sessionId));
    }
  };

// restartOrchestrator refocuses by the new serial; the old one is a dead PTY.
export const restartOrchestrator =
  (id: string): AppThunk<Promise<void>> =>
  async (dispatch) => {
    try {
      const info = await RestartOrchestrator(id, 80, 24);
      dispatch(focusOrchestratorSession(info.sessionId));
      await dispatch(loadOrchestrators());
    } catch (error) {
      dispatch(setOrchestratorsError(readError(error)));
    }
  };

// restartApp does not interrupt orchestrators; they run as background agents.
export const restartApp =
  (returnToOrchestratorId: string): AppThunk<Promise<void>> =>
  async (dispatch) => {
    try {
      await RestartApp(returnToOrchestratorId);
    } catch (error) {
      dispatch(setOrchestratorsError(readError(error)));
    }
  };

// restoreOpenOrchestrators returns whether one took the pane, so boot skips the
// default env selection. Every other open orchestrator reopens off the pane.
export const restoreOpenOrchestrators =
  (): AppThunk<Promise<boolean>> => async (dispatch, getState) => {
    try {
      const target = await ResolveOrchestratorToReopen();
      const { primary, alsoReopen } = planOrchestratorRestore(
        target,
        getState().orchestrators.items,
      );
      if (!primary && alsoReopen.length === 0) {
        return false;
      }

      for (const id of alsoReopen) {
        try {
          await StartOrchestrator(id, 80, 24);
        } catch (error) {
          dispatch(setOrchestratorsError(readError(error)));
        }
      }

      let restoredPane = false;
      if (primary) {
        const info = await StartOrchestrator(primary, 80, 24);
        dispatch(focusOrchestratorSession(info.sessionId));
        restoredPane = true;
      }
      await dispatch(loadOrchestrators());
      return restoredPane;
    } catch (error) {
      dispatch(setOrchestratorsError(readError(error)));
      return false;
    }
  };

export const stopOrchestrator =
  (id: string): AppThunk<Promise<void>> =>
  async (dispatch) => {
    try {
      await StopOrchestrator(id);
    } catch (error) {
      dispatch(setOrchestratorsError(readError(error)));
    }
    await dispatch(loadOrchestrators());
  };

// deleteOrchestrator leaves the linked environments' sync config intact.
export const deleteOrchestrator =
  (id: string): AppThunk<Promise<void>> =>
  async (dispatch) => {
    dispatch(setOrchestratorsBusy(true));
    try {
      await DeleteOrchestrator(id);
      dispatch(closeOrchestratorDialog());
      await dispatch(loadOrchestrators());
      dispatch(setOrchestratorsBusy(false));
    } catch (error) {
      dispatch(setOrchestratorsError(readError(error)));
    }
  };

export const revealOrchestratorGuidance =
  (id: string, layer: OrchestratorGuidanceLayer, ide: IDEKind): AppThunk<Promise<void>> =>
  async (dispatch) => {
    try {
      await RevealOrchestratorGuidance(id, layer, ide);
    } catch (error) {
      dispatch(setOrchestratorsError(readError(error)));
    }
  };

export const investigateFailure =
  (report: string, tenant: string, environment: string): AppThunk<Promise<void>> =>
  async (dispatch) => {
    try {
      const info = await InvestigateFailure(report, tenant, environment, 80, 24);
      dispatch(focusOrchestratorSession(info.sessionId));
      await dispatch(loadOrchestrators());
    } catch (error) {
      dispatch(setOrchestratorsError(readError(error)));
    }
  };

// reportFailure focuses an existing draft for the same failure; any other refusal
// falls back to the prefilled URL, naming the refusal.
export const reportFailure =
  (report: string, summary: string, tenant: string, environment: string): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    try {
      const outcome = await ReportBugFailure(report, tenant, environment, 80, 24);
      if (outcome.admitted) {
        dispatch(focusOrchestratorSession(outcome.orchestrator.sessionId));
        await dispatch(loadOrchestrators());
        return;
      }
      if (outcome.existingId) {
        await dispatch(loadOrchestrators());
        const existing = getState().orchestrators.items.find(
          (item) => item.id === outcome.existingId,
        );
        if (existing) {
          dispatch(focusOrchestratorSession(existing.sessionId));
        }
        dispatch(
          showNotification(
            'info',
            outcome.message ?? 'Already drafting a report for this failure.',
            { tenant, environment },
          ),
        );
        return;
      }
      dispatch(openFallbackReportURL(report, summary, tenant, environment, outcome.message ?? ''));
    } catch (error) {
      dispatch(openFallbackReportURL(report, summary, tenant, environment, readError(error)));
    }
  };

const openFallbackReportURL =
  (
    report: string,
    summary: string,
    tenant: string,
    environment: string,
    reason: string,
  ): AppThunk =>
  (dispatch, getState) => {
    const build =
      stateApi.endpoints.getInitialState.select(undefined)(getState()).data?.build ?? null;
    const title = tenant && environment ? `${tenant}/${environment}: ${summary}` : summary;
    const body = diagnosticsIssueBody(report, build);
    const { url } = buildDiagnosticsIssueURL(title, body);
    BrowserOpenURL(url);
    void ClipboardSetText(report);
    const prefix = reason ? `${reason} ` : '';
    dispatch(
      showNotification(
        'warning',
        `${prefix}Opened a prefilled issue in the browser instead — the full report is on your clipboard.`,
        { tenant, environment },
      ),
    );
  };
