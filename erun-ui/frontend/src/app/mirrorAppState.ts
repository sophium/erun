import { setEnvironmentDialog } from './slices/environmentDialogSlice';
import { setGlobalConfigDialog } from './slices/globalConfigDialogSlice';
import { setIdleAll } from './slices/idleSlice';
import { setLayoutAll } from './slices/layoutSlice';
import { setManageDialog } from './slices/manageDialogSlice';
import { setNotificationAll } from './slices/notificationSlice';
import { setReviewAll } from './slices/reviewSlice';
import { setSelectionAll } from './slices/selectionSlice';
import { setSidebarAll } from './slices/sidebarSlice';
import { setTenantDashboard } from './slices/tenantDashboardSlice';
import { setTenantDialog } from './slices/tenantDialogSlice';
import { setTenantsAll } from './slices/tenantsSlice';
import { setTerminalAll } from './slices/terminalSlice';
import { setTerminalStatusAll } from './slices/terminalStatusSlice';
import { setDoctorAll } from './slices/doctorSlice';
import type { AppState } from './state';
import type { AppDispatch, RootState } from './store';

// mirrorAppStateToRedux pushes the legacy ERunUIController state shape into
// the discrete Redux slices. ERunUIController invokes this from its emit()
// hook so the Redux store stays a faithful mirror while individual writes
// continue to use the existing this.state.foo = bar mutations. Subsequent
// PRs can replace those mutations with direct dispatches and remove the
// mirror once every owner has moved.
export function mirrorAppStateToRedux(
  dispatch: AppDispatch,
  prev: RootState,
  state: AppState,
): void {
  mirrorSelection(dispatch, prev, state);
  mirrorTenants(dispatch, prev, state);
  mirrorDialogs(dispatch, prev, state);
  mirrorSidebar(dispatch, prev, state);
  mirrorTerminal(dispatch, prev, state);
  mirrorTerminalStatus(dispatch, prev, state);
  mirrorLayout(dispatch, prev, state);
  mirrorReview(dispatch, prev, state);
  mirrorNotification(dispatch, prev, state);
  mirrorIdle(dispatch, prev, state);
  mirrorDoctor(dispatch, prev, state);
}

function mirrorSelection(dispatch: AppDispatch, prev: RootState, state: AppState): void {
  if (state.selected !== prev.selection.selected) {
    dispatch(setSelectionAll({ selected: state.selected }));
  }
}

function mirrorTenants(dispatch: AppDispatch, prev: RootState, state: AppState): void {
  if (
    state.tenants === prev.tenants.tenants &&
    state.cloudProviders === prev.tenants.cloudProviders &&
    state.versionSuggestions === prev.tenants.versionSuggestions
  ) {
    return;
  }
  dispatch(
    setTenantsAll({
      tenants: state.tenants,
      cloudProviders: state.cloudProviders,
      versionSuggestions: state.versionSuggestions,
    }),
  );
}

function mirrorDialogs(dispatch: AppDispatch, prev: RootState, state: AppState): void {
  if (state.environmentDialog !== prev.environmentDialog) {
    dispatch(setEnvironmentDialog(state.environmentDialog));
  }
  if (state.manageDialog !== prev.manageDialog) {
    dispatch(setManageDialog(state.manageDialog));
  }
  if (state.tenantDialog !== prev.tenantDialog) {
    dispatch(setTenantDialog(state.tenantDialog));
  }
  if (state.tenantDashboard !== prev.tenantDashboard) {
    dispatch(setTenantDashboard(state.tenantDashboard));
  }
  if (state.globalConfigDialog !== prev.globalConfigDialog) {
    dispatch(setGlobalConfigDialog(state.globalConfigDialog));
  }
}

function mirrorSidebar(dispatch: AppDispatch, prev: RootState, state: AppState): void {
  const collapsedArr = Array.from(state.collapsedTenants);
  if (
    arraysEqual(collapsedArr, prev.sidebar.collapsedTenants) &&
    state.sidebarCloudAliasBusy === prev.sidebar.sidebarCloudAliasBusy &&
    state.sidebarCloudAliasAction === prev.sidebar.sidebarCloudAliasAction
  ) {
    return;
  }
  dispatch(
    setSidebarAll({
      collapsedTenants: collapsedArr,
      sidebarCloudAliasBusy: state.sidebarCloudAliasBusy,
      sidebarCloudAliasAction: state.sidebarCloudAliasAction,
    }),
  );
}

function mirrorTerminal(dispatch: AppDispatch, prev: RootState, state: AppState): void {
  if (
    state.sessionId === prev.terminal.sessionId &&
    state.tabsByEnv === prev.terminal.tabsByEnv &&
    state.selectedSessionByEnv === prev.terminal.selectedSessionByEnv &&
    state.debugOutput === prev.terminal.debugOutput
  ) {
    return;
  }
  dispatch(
    setTerminalAll({
      sessionId: state.sessionId,
      tabsByEnv: state.tabsByEnv,
      selectedSessionByEnv: state.selectedSessionByEnv,
      debugOutput: state.debugOutput,
    }),
  );
}

function mirrorTerminalStatus(dispatch: AppDispatch, prev: RootState, state: AppState): void {
  const previous = prev.terminalStatus;
  if (
    state.terminalMessage === previous.terminalMessage &&
    state.terminalStatusKind === previous.terminalStatusKind &&
    state.terminalStatusDetail === previous.terminalStatusDetail &&
    state.terminalStatusAction === previous.terminalStatusAction &&
    state.terminalBusy === previous.terminalBusy &&
    state.terminalCopyOutput === previous.terminalCopyOutput &&
    state.terminalCopyStatus === previous.terminalCopyStatus
  ) {
    return;
  }
  dispatch(
    setTerminalStatusAll({
      terminalMessage: state.terminalMessage,
      terminalStatusKind: state.terminalStatusKind,
      terminalStatusDetail: state.terminalStatusDetail,
      terminalStatusAction: state.terminalStatusAction,
      terminalBusy: state.terminalBusy,
      terminalCopyOutput: state.terminalCopyOutput,
      terminalCopyStatus: state.terminalCopyStatus,
    }),
  );
}

function mirrorLayout(dispatch: AppDispatch, prev: RootState, state: AppState): void {
  const previous = prev.layout;
  if (
    state.sidebarWidth === previous.sidebarWidth &&
    state.reviewWidth === previous.reviewWidth &&
    state.filesWidth === previous.filesWidth &&
    state.filesOpen === previous.filesOpen &&
    state.sidebarHidden === previous.sidebarHidden &&
    state.reviewOpen === previous.reviewOpen &&
    state.changedFilesOpen === previous.changedFilesOpen &&
    state.debugOpen === previous.debugOpen &&
    state.debugHeight === previous.debugHeight
  ) {
    return;
  }
  dispatch(
    setLayoutAll({
      sidebarWidth: state.sidebarWidth,
      reviewWidth: state.reviewWidth,
      filesWidth: state.filesWidth,
      filesOpen: state.filesOpen,
      sidebarHidden: state.sidebarHidden,
      reviewOpen: state.reviewOpen,
      changedFilesOpen: state.changedFilesOpen,
      debugOpen: state.debugOpen,
      debugHeight: state.debugHeight,
    }),
  );
}

function mirrorReview(dispatch: AppDispatch, prev: RootState, state: AppState): void {
  const collapsedDirsArr = Array.from(state.collapsedDiffDirs);
  if (reviewMatches(prev, state, collapsedDirsArr)) {
    return;
  }
  dispatch(
    setReviewAll({
      diff: state.diff,
      diffLoading: state.diffLoading,
      diffError: state.diffError,
      diffErrorReconnectable: state.diffErrorReconnectable,
      reconnect: state.reconnect,
      selectedDiffPath: state.selectedDiffPath,
      selectedReviewScope: state.selectedReviewScope,
      selectedReviewCommit: state.selectedReviewCommit,
      diffFilter: state.diffFilter,
      collapsedDiffDirs: collapsedDirsArr,
    }),
  );
}

function reviewMatches(prev: RootState, state: AppState, collapsedDirsArr: string[]): boolean {
  const previous = prev.review;
  return (
    state.diff === previous.diff &&
    state.diffLoading === previous.diffLoading &&
    state.diffError === previous.diffError &&
    state.diffErrorReconnectable === previous.diffErrorReconnectable &&
    state.reconnect === previous.reconnect &&
    state.selectedDiffPath === previous.selectedDiffPath &&
    state.selectedReviewScope === previous.selectedReviewScope &&
    state.selectedReviewCommit === previous.selectedReviewCommit &&
    state.diffFilter === previous.diffFilter &&
    arraysEqual(collapsedDirsArr, previous.collapsedDiffDirs)
  );
}

function mirrorNotification(dispatch: AppDispatch, prev: RootState, state: AppState): void {
  if (state.notification !== prev.notification.notification) {
    dispatch(setNotificationAll({ notification: state.notification }));
  }
}

function mirrorIdle(dispatch: AppDispatch, prev: RootState, state: AppState): void {
  if (
    state.idleStatus === prev.idle.idleStatus &&
    state.idleCloudContextBusy === prev.idle.idleCloudContextBusy
  ) {
    return;
  }
  dispatch(
    setIdleAll({
      idleStatus: state.idleStatus,
      idleCloudContextBusy: state.idleCloudContextBusy,
    }),
  );
}

function mirrorDoctor(dispatch: AppDispatch, prev: RootState, state: AppState): void {
  if (state.lastDoctorBySelection !== prev.doctor.lastDoctorBySelection) {
    dispatch(setDoctorAll({ lastDoctorBySelection: state.lastDoctorBySelection }));
  }
}

function arraysEqual(a: readonly string[], b: readonly string[]): boolean {
  if (a === b) return true;
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i += 1) {
    if (a[i] !== b[i]) return false;
  }
  return true;
}
