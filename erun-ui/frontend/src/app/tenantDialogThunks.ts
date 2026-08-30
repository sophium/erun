import {
  INVITE_REQUEST_KIND_JOIN_TENANT,
  type UITenant,
  type UITenantConfig,
  type UITenantDashboardInput,
} from '@/types';

import { cloudApi } from './api/cloudApi';
import { tenantApi } from './api/tenantApi';
import { replaceCloudProvider } from './cloudContextState';
import { readError } from './errors';
import { showNotification, showTerminalError, showTerminalMessage } from './notificationThunks';
import { setIdleStatus } from './slices/idleSlice';
import { setReviewOpen } from './slices/layoutSlice';
import { setSelected } from './slices/selectionSlice';
import { patchTenantDashboard, setTenantDashboard } from './slices/tenantDashboardSlice';
import { patchTenantDialog, setTenantDialog } from './slices/tenantDialogSlice';
import { setCloudProviders, setTenants } from './slices/tenantsSlice';
import {
  defaultReviewFilter,
  defaultTenantDialog,
  type ReviewFilterState,
  type TenantDashboardState,
  type TenantDashboardTab,
  type TenantDialogState,
} from './state';
import type { AppThunk } from './store';
import { defaultRegistrationState } from './tenantRegistrationState';
import { requireController } from './thunkExtra';

export const openTenantDialog =
  (tenant: string): AppThunk =>
  (dispatch) => {
    dispatch(
      setTenantDialog({
        open: true,
        tenant,
        config: {
          name: tenant,
          defaultEnvironment: '',
          apiUrl: '',
          cloudProviderAliases: [],
          primaryCloudProviderAlias: '',
          cloudProviders: [],
        },
        configLoading: true,
        busy: false,
        busyAction: '',
        busyTarget: '',
        error: '',
      }),
    );
    void dispatch(loadTenantConfig());
  };

export const closeTenantDialog = (): AppThunk => (dispatch, getState, extra) => {
  const controller = requireController(extra);
  if (getState().tenantDialog.busy) {
    return;
  }
  dispatch(setTenantDialog(defaultTenantDialog()));
  controller.focusTerminalSoon();
};

export const updateTenantDialog =
  (values: Partial<TenantDialogState>): AppThunk =>
  (dispatch, getState) => {
    if (getState().tenantDialog.busy) {
      return;
    }
    dispatch(
      patchTenantDialog({
        ...values,
        error: values.error ?? '',
      }),
    );
  };

export const updateTenantConfig =
  (values: Partial<UITenantConfig>): AppThunk =>
  (dispatch, getState) => {
    const dialog = getState().tenantDialog;
    if (dialog.busy || dialog.configLoading) {
      return;
    }
    dispatch(
      updateTenantDialog({
        config: {
          ...dialog.config,
          ...values,
        },
      }),
    );
  };

export const loadTenantConfig = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const dialog = getState().tenantDialog;
  if (!dialog.open || !dialog.tenant) {
    return;
  }
  dispatch(
    patchTenantDialog({
      configLoading: true,
      error: '',
    }),
  );
  try {
    const result = await dispatch(
      tenantApi.endpoints.getTenantConfig.initiate(dialog.tenant),
    ).unwrap();
    if (result.cloudProviders) {
      dispatch(setCloudProviders(result.cloudProviders));
    }
    dispatch(
      patchTenantDialog({
        config: result,
        configLoading: false,
        error: '',
      }),
    );
  } catch (error) {
    dispatch(
      patchTenantDialog({
        configLoading: false,
        error: readError(error),
      }),
    );
  }
};

export const submitTenantConfig = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const dialog = getState().tenantDialog;
  if (dialog.busy || dialog.configLoading) {
    return;
  }
  if (!dialog.tenant) {
    dispatch(closeTenantDialog());
    return;
  }
  dispatch(patchTenantDialog({ busy: true, busyAction: 'save', busyTarget: '', error: '' }));
  try {
    const result = await dispatch(
      tenantApi.endpoints.saveTenantConfig.initiate(dialog.config),
    ).unwrap();
    applySavedTenantConfig(dispatch, getState, result);
    dispatch(
      patchTenantDialog({
        config: result,
        busy: false,
        busyAction: '',
        busyTarget: '',
        error: '',
      }),
    );
    dispatch(showNotification('success', `Saved config for ${result.name}.`));
    dispatch(closeTenantDialog());
  } catch (error) {
    const message = readError(error);
    dispatch(
      patchTenantDialog({
        busy: false,
        busyAction: '',
        busyTarget: '',
        error: message,
      }),
    );
    dispatch(showTerminalError(message));
  }
};

export const setupTenantCloudProviderOIDC =
  (alias: string): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    alias = alias.trim();
    const dialog = getState().tenantDialog;
    if (!alias || dialog.busy || dialog.configLoading) {
      return;
    }
    dispatch(
      patchTenantDialog({ busy: true, busyAction: 'cloud-oidc', busyTarget: alias, error: '' }),
    );
    try {
      const provider = await dispatch(
        cloudApi.endpoints.setupCloudProviderOIDC.initiate(alias),
      ).unwrap();
      dispatch(
        setCloudProviders(replaceCloudProvider(getState().tenants.cloudProviders, provider)),
      );
      const currentDialog = getState().tenantDialog;
      const currentProviders = currentDialog.config.cloudProviders ?? [];
      dispatch(
        patchTenantDialog({
          config: {
            ...currentDialog.config,
            cloudProviders: replaceCloudProvider(currentProviders, provider),
          },
          busy: false,
          busyAction: '',
          busyTarget: '',
          error: '',
        }),
      );
      dispatch(showNotification('success', `Updated OIDC issuer for ${provider.alias}.`));
    } catch (error) {
      const message = readError(error);
      dispatch(
        patchTenantDialog({
          busy: false,
          busyAction: '',
          busyTarget: '',
          error: message,
        }),
      );
      dispatch(showTerminalError(message));
      dispatch(showNotification('error', message));
    }
  };

function applySavedTenantConfig(
  dispatch: (
    action: ReturnType<typeof setTenants> | ReturnType<typeof setCloudProviders>,
  ) => unknown,
  getState: () => ReturnType<typeof import('./store').store.getState>,
  config: UITenantConfig,
): void {
  const tenantName = config.name.trim();
  if (!tenantName) {
    return;
  }
  const currentTenants = getState().tenants.tenants;
  dispatch(
    setTenants(
      currentTenants.map((tenant) => {
        if (tenant.name !== tenantName) {
          return tenant;
        }
        return {
          ...tenant,
          cloudProviderAliases: config.cloudProviderAliases ?? [],
          primaryCloudProviderAlias: config.primaryCloudProviderAlias ?? '',
        };
      }),
    ),
  );
  if (config.cloudProviders) {
    dispatch(setCloudProviders(config.cloudProviders));
  }
}

// inviteRequestDraftCarryOver keeps the request dialog's in-progress draft
// (kind/note/rate-limit countdown) when reopening the same tenant's
// dashboard, and resets it for a different one — split out of
// openTenantDashboard so that thunk's own branching stays under the module's
// complexity cap.
function inviteRequestDraftCarryOver(
  sameTenant: boolean,
  currentDashboard: TenantDashboardState,
): Pick<
  TenantDashboardState,
  'requestKindDraft' | 'requestNoteDraft' | 'requestRateLimitedUntil' | 'issuedInviteLink'
> {
  if (sameTenant) {
    return {
      requestKindDraft: currentDashboard.requestKindDraft,
      requestNoteDraft: currentDashboard.requestNoteDraft,
      requestRateLimitedUntil: currentDashboard.requestRateLimitedUntil,
      issuedInviteLink: currentDashboard.issuedInviteLink,
    };
  }
  return {
    requestKindDraft: INVITE_REQUEST_KIND_JOIN_TENANT,
    requestNoteDraft: '',
    requestRateLimitedUntil: 0,
    issuedInviteLink: null,
  };
}

export const openTenantDashboard =
  (tenant: string): AppThunk =>
  (dispatch, getState) => {
    tenant = tenant.trim();
    if (!tenant) {
      return;
    }
    const currentDashboard = getState().tenantDashboard;
    const sameTenant = currentDashboard.tenant === tenant;
    dispatch(setSelected(null));
    dispatch(setIdleStatus(null));
    dispatch(
      setTenantDashboard({
        tenant,
        tab: sameTenant ? currentDashboard.tab : 'users',
        loading: true,
        error: '',
        data: null,
        reviewFilter: sameTenant ? currentDashboard.reviewFilter : defaultReviewFilter(),
        platformAliasOverride: sameTenant ? currentDashboard.platformAliasOverride : '',
        connectApiUrlDraft: sameTenant ? currentDashboard.connectApiUrlDraft : '',
        connecting: false,
        connectError: '',
        enrollUsernameDraft: sameTenant ? currentDashboard.enrollUsernameDraft : '',
        enrolling: false,
        enrollError: '',
        registration: sameTenant ? currentDashboard.registration : defaultRegistrationState(),
        requestDialogOpen: false,
        requesting: false,
        requestError: '',
        decliningInviteRequestId: '',
        declineReasonDraft: '',
        decidingInviteRequestId: '',
        decideInviteRequestError: '',
        ...inviteRequestDraftCarryOver(sameTenant, currentDashboard),
      }),
    );
    dispatch(setReviewOpen(false));
    dispatch(showTerminalMessage(''));
    void dispatch(loadTenantDashboard(tenant));
  };

export const setTenantDashboardTab =
  (tab: TenantDashboardTab): AppThunk =>
  (dispatch) => {
    dispatch(patchTenantDashboard({ tab }));
  };

// setReviewFilter applies a Reviews-tab discovery filter and reloads the
// dashboard so the new filter reaches the platform read, not just local
// state — matching every other tenant-dashboard filter's the-list-is-the-
// state-of-the-world contract.
export const setReviewFilter =
  (next: Partial<ReviewFilterState>): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const reviewFilter = { ...getState().tenantDashboard.reviewFilter, ...next };
    dispatch(patchTenantDashboard({ reviewFilter }));
    await dispatch(loadTenantDashboard());
  };

export const loadTenantDashboard =
  (tenant?: string): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const state = getState();
    const target = (tenant ?? state.tenantDashboard.tenant).trim();
    if (!target || state.tenantDashboard.tenant !== target) {
      return;
    }
    const tenantState = state.tenants.tenants.find((candidate) => candidate.name === target);
    const input = tenantDashboardInput(
      target,
      tenantState,
      state.tenantDashboard.reviewFilter,
      state.tenantDashboard.platformAliasOverride,
    );
    dispatch(patchTenantDashboard({ loading: true, error: '' }));
    // forceRefetch: true, because this fires on every dashboard open and every
    // Refresh click — without it RTK Query's cache (never invalidated here)
    // just replays the first successful result for the life of the process.
    // unsubscribe once consumed so the one-shot call doesn't pin that cache
    // entry open forever either.
    const request = dispatch(
      tenantApi.endpoints.getTenantDashboard.initiate(input, { forceRefetch: true }),
    );
    try {
      const loadedData = await request.unwrap();
      if (getState().tenantDashboard.tenant !== target) {
        return;
      }
      const data = { ...loadedData, environment: loadedData.environment ?? input.environment };
      dispatch(
        patchTenantDashboard({
          loading: false,
          error: '',
          data,
        }),
      );
    } catch (error) {
      if (getState().tenantDashboard.tenant !== target) {
        return;
      }
      dispatch(
        patchTenantDashboard({
          loading: false,
          error: readError(error),
          data: null,
        }),
      );
    } finally {
      request.unsubscribe();
    }
  };

export const refreshTenantDashboard = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const tenant = getState().tenantDashboard.tenant;
  await dispatch(loadTenantDashboard(tenant));
  const after = getState().tenantDashboard;
  if (after.tenant === tenant && !after.error) {
    dispatch(showNotification('success', 'Dashboard refreshed.'));
  }
};

// chooseTenantPlatformAlias resolves the choose-alias state: more than one
// erun-type platform alias is configured and the operator picked one. The
// choice is kept in tenantDashboard state (not persisted) so it survives
// tab switches for this session but resets the next time this tenant's
// dashboard is opened fresh.
export const chooseTenantPlatformAlias =
  (alias: string): AppThunk<Promise<void>> =>
  async (dispatch) => {
    dispatch(patchTenantDashboard({ platformAliasOverride: alias.trim() }));
    await dispatch(loadTenantDashboard());
  };

// tenantDashboardInput no longer resolves a platform base URL or cloud
// alias itself — the desktop's Go side resolves the erun-type platform
// alias the same way `erun platform` does and reports the outcome on the
// response (platformState/platformAlias/platformUrl). This only still needs
// an environment for the API-log panel's own MCP/kube-context read, which is
// a distinct, per-environment concern.
function tenantDashboardInput(
  tenantName: string,
  tenant: UITenant | undefined,
  reviewFilter: ReviewFilterState,
  platformAliasOverride: string,
): UITenantDashboardInput {
  const environment = tenant ? tenantDashboardEnvironment(tenant) : undefined;
  return {
    tenant: tenantName,
    environment: trimOptional(environment?.name),
    mcpUrl: trimOptional(environment?.mcpUrl),
    kubernetesContext: trimOptional(environment?.kubernetesContext),
    platformAlias: platformAliasOverride,
    reviewFilterMine: reviewFilter.mine,
    reviewFilterWaitingOnMe: reviewFilter.waitingOnMe,
  };
}

function tenantDashboardEnvironment(
  tenant: UITenant,
): UITenant['environments'][number] | undefined {
  const defaultEnvironment = tenant.defaultEnvironment?.trim();
  return (
    tenant.environments.find(
      (candidate) => candidate.name === defaultEnvironment && candidate.apiUrl,
    ) ?? tenant.environments.find((candidate) => candidate.apiUrl)
  );
}

function trimOptional(value: string | undefined): string {
  return value?.trim() ?? '';
}
