import { selectAppState } from './appStateSelector';
import { setEnvironmentDialog } from './slices/environmentDialogSlice';
import { setGlobalConfigDialog } from './slices/globalConfigDialogSlice';
import { setIdleCloudContextBusy, setIdleStatus } from './slices/idleSlice';
import {
  setChangedFilesOpen,
  setDebugHeight,
  setDebugOpen,
  setFilesOpen,
  setFilesWidth,
  setReviewOpen,
  setReviewWidth,
  setSidebarHidden,
  setSidebarWidth,
} from './slices/layoutSlice';
import { setManageDialog } from './slices/manageDialogSlice';
import { dismissNotification, showNotification } from './slices/notificationSlice';
import {
  setDiff,
  setDiffError,
  setDiffFilter,
  setDiffLoading,
  setReconnect,
  setSelectedDiffPath,
  setSelectedReviewScope,
  setSelectedReviewCommit,
  setReviewAll,
} from './slices/reviewSlice';
import { setSelected } from './slices/selectionSlice';
import {
  setSidebarAll,
  setSidebarCloudAliasBusy,
} from './slices/sidebarSlice';
import { setTenantDashboard } from './slices/tenantDashboardSlice';
import { setTenantDialog } from './slices/tenantDialogSlice';
import {
  setCloudProviders,
  setTenants,
  setVersionSuggestions,
} from './slices/tenantsSlice';
import {
  setDebugOutput,
  setSessionId,
  setTabsForEnv as _setTabsForEnv,
  setTerminalAll,
} from './slices/terminalSlice';
import {
  setTerminalCopyOutput,
  setTerminalCopyStatus,
  setTerminalMessage,
} from './slices/terminalStatusSlice';
import { setDoctorAll } from './slices/doctorSlice';
import type { AppState, ReconnectState, TerminalTab } from './state';
import type { AppDispatch, RootState } from './store';
import type {
  DiffResult,
  UICloudProviderStatus,
  UIIdleStatus,
  UISelection,
  UITenant,
  UIVersionSuggestion,
} from '@/types';

type StoreLike = { dispatch: AppDispatch; getState: () => RootState };
type FieldSetters = { [K in keyof AppState]?: (value: AppState[K]) => void };

function buildSelectionSetters(store: StoreLike): FieldSetters {
  return {
    selected: (v) => store.dispatch(setSelected(v as UISelection | null)),
    tenants: (v) => store.dispatch(setTenants(v as UITenant[])),
    cloudProviders: (v) => store.dispatch(setCloudProviders(v as UICloudProviderStatus[])),
    versionSuggestions: (v) => store.dispatch(setVersionSuggestions(v as UIVersionSuggestion[])),
  };
}

function buildDialogSetters(store: StoreLike): FieldSetters {
  return {
    environmentDialog: (v) => store.dispatch(setEnvironmentDialog(v)),
    manageDialog: (v) => store.dispatch(setManageDialog(v)),
    tenantDialog: (v) => store.dispatch(setTenantDialog(v)),
    tenantDashboard: (v) => store.dispatch(setTenantDashboard(v)),
    globalConfigDialog: (v) => store.dispatch(setGlobalConfigDialog(v)),
  };
}

function buildSidebarSetters(store: StoreLike): FieldSetters {
  return {
    collapsedTenants: (v) => {
      store.dispatch(
        setSidebarAll({
          collapsedTenants: Array.from(v as Set<string>),
          sidebarCloudAliasBusy: store.getState().sidebar.sidebarCloudAliasBusy,
          sidebarCloudAliasAction: store.getState().sidebar.sidebarCloudAliasAction,
        }),
      );
    },
    sidebarCloudAliasBusy: (v) => {
      const current = store.getState().sidebar;
      store.dispatch(setSidebarCloudAliasBusy({ busy: v as boolean, action: current.sidebarCloudAliasAction }));
    },
    sidebarCloudAliasAction: (v) => {
      const current = store.getState().sidebar;
      store.dispatch(
        setSidebarCloudAliasBusy({
          busy: current.sidebarCloudAliasBusy,
          action: v as RootState['sidebar']['sidebarCloudAliasAction'],
        }),
      );
    },
  };
}

function buildTerminalSetters(store: StoreLike): FieldSetters {
  return {
    sessionId: (v) => store.dispatch(setSessionId(v as number)),
    tabsByEnv: (v) => {
      const record = v as Record<string, TerminalTab[]>;
      for (const [key, tabs] of Object.entries(record)) {
        store.dispatch(_setTabsForEnv({ key, tabs }));
      }
      const previous = store.getState().terminal.tabsByEnv;
      for (const key of Object.keys(previous)) {
        if (!(key in record)) {
          store.dispatch(_setTabsForEnv({ key, tabs: [] }));
        }
      }
    },
    selectedSessionByEnv: (v) => {
      const record = v as Record<string, number>;
      const previous = store.getState().terminal;
      store.dispatch(
        setTerminalAll({
          sessionId: previous.sessionId,
          tabsByEnv: previous.tabsByEnv,
          selectedSessionByEnv: { ...record },
          debugOutput: previous.debugOutput,
        }),
      );
    },
    debugOutput: (v) => store.dispatch(setDebugOutput(v as string)),
  };
}

function buildLayoutSetters(store: StoreLike): FieldSetters {
  return {
    sidebarWidth: (v) => store.dispatch(setSidebarWidth(v as number)),
    reviewWidth: (v) => store.dispatch(setReviewWidth(v as number)),
    filesWidth: (v) => store.dispatch(setFilesWidth(v as number)),
    filesOpen: (v) => store.dispatch(setFilesOpen(v as boolean)),
    sidebarHidden: (v) => store.dispatch(setSidebarHidden(v as boolean)),
    reviewOpen: (v) => store.dispatch(setReviewOpen(v as boolean)),
    changedFilesOpen: (v) => store.dispatch(setChangedFilesOpen(v as boolean)),
    debugOpen: (v) => store.dispatch(setDebugOpen(v as boolean)),
    debugHeight: (v) => store.dispatch(setDebugHeight(v as number)),
  };
}

function buildReviewSetters(store: StoreLike): FieldSetters {
  return {
    diff: (v) => store.dispatch(setDiff(v as DiffResult | null)),
    diffLoading: (v) => store.dispatch(setDiffLoading(v as boolean)),
    diffError: (v) =>
      store.dispatch(
        setDiffError({ error: v as string, reconnectable: store.getState().review.diffErrorReconnectable }),
      ),
    diffErrorReconnectable: (v) =>
      store.dispatch(
        setDiffError({ error: store.getState().review.diffError, reconnectable: v as boolean }),
      ),
    reconnect: (v) => store.dispatch(setReconnect(v as ReconnectState)),
    selectedDiffPath: (v) => store.dispatch(setSelectedDiffPath(v as string)),
    selectedReviewScope: (v) => store.dispatch(setSelectedReviewScope(v as 'current' | 'commit' | 'all')),
    selectedReviewCommit: (v) => store.dispatch(setSelectedReviewCommit(v as string)),
    diffFilter: (v) => store.dispatch(setDiffFilter(v as string)),
    collapsedDiffDirs: (v) => {
      const arr = Array.from(v as Set<string>);
      const current = store.getState().review;
      store.dispatch(setReviewAll({ ...current, collapsedDiffDirs: arr }));
    },
  };
}

function buildTerminalStatusSetters(store: StoreLike): FieldSetters {
  const dispatchStatus = (overrides: Partial<RootState['terminalStatus']>) => {
    const current = store.getState().terminalStatus;
    store.dispatch(
      setTerminalMessage({
        message: overrides.terminalMessage ?? current.terminalMessage,
        busy: overrides.terminalBusy ?? current.terminalBusy,
        kind: overrides.terminalStatusKind ?? current.terminalStatusKind,
        detail: overrides.terminalStatusDetail ?? current.terminalStatusDetail,
        actionKind: overrides.terminalStatusAction ?? current.terminalStatusAction,
      }),
    );
  };
  return {
    terminalMessage: (v) => dispatchStatus({ terminalMessage: v as string }),
    terminalStatusKind: (v) =>
      dispatchStatus({ terminalStatusKind: v as RootState['terminalStatus']['terminalStatusKind'] }),
    terminalStatusDetail: (v) => dispatchStatus({ terminalStatusDetail: v as string }),
    terminalStatusAction: (v) =>
      dispatchStatus({ terminalStatusAction: v as RootState['terminalStatus']['terminalStatusAction'] }),
    terminalBusy: (v) => dispatchStatus({ terminalBusy: v as boolean }),
    terminalCopyOutput: (v) => store.dispatch(setTerminalCopyOutput(v as string)),
    terminalCopyStatus: (v) => store.dispatch(setTerminalCopyStatus(v as string)),
  };
}

function buildMiscSetters(store: StoreLike): FieldSetters {
  return {
    notification: (v) => {
      if (v) store.dispatch(showNotification(v));
      else store.dispatch(dismissNotification());
    },
    idleStatus: (v) => store.dispatch(setIdleStatus(v as UIIdleStatus | null)),
    idleCloudContextBusy: (v) => store.dispatch(setIdleCloudContextBusy(v as boolean)),
    lastDoctorBySelection: (v) =>
      store.dispatch(setDoctorAll({ lastDoctorBySelection: v as AppState['lastDoctorBySelection'] })),
  };
}

// createControllerStateProxy returns an object that LOOKS like a mutable
// AppState to ERunUIController and the workflow classes, but whose reads
// come from the Redux store and whose writes dispatch the matching slice
// action. This lets the 200+ existing this.state.foo = bar mutations stay
// in place while making Redux the canonical owner. The proxy keeps the
// legacy AppState shape (including Set<string>) by delegating reads to
// selectAppState.
export function createControllerStateProxy(store: StoreLike): AppState {
  const setterByField: FieldSetters = {
    ...buildSelectionSetters(store),
    ...buildDialogSetters(store),
    ...buildSidebarSetters(store),
    ...buildTerminalSetters(store),
    ...buildLayoutSetters(store),
    ...buildReviewSetters(store),
    ...buildTerminalStatusSetters(store),
    ...buildMiscSetters(store),
  };

  return new Proxy({} as AppState, {
    get: (_target, prop) => {
      const snapshot = selectAppState(store.getState());
      return snapshot[prop as keyof AppState];
    },
    set: (_target, prop, value) => {
      const setter = setterByField[prop as keyof AppState];
      if (!setter) {
        throw new Error(`controllerStateProxy: no setter registered for AppState.${String(prop)}`);
      }
      (setter as (value: unknown) => void)(value);
      return true;
    },
  });
}
