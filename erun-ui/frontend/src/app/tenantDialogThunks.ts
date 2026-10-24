import {
  INVITE_REQUEST_KIND_JOIN_TENANT,
  TENANT_PLATFORM_STATE_CHOOSE_ALIAS,
  TENANT_PLATFORM_STATE_NOT_CONNECTED,
  TENANT_PLATFORM_STATE_NOT_SIGNED_IN,
  TENANT_PLATFORM_STATE_TENANT_MISMATCH,
  type UITenant,
  type UITenantConfig,
  type UITenantDashboardInput,
} from '@/types';

import { cloudApi } from './api/cloudApi';
import { tenantApi } from './api/tenantApi';
import { tenantInviteRequestApi } from './api/tenantInviteRequestApi';
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
import type { AppDispatch, AppThunk, RootState } from './store';
import { defaultRegistrationState } from './tenantRegistrationState';
import { updateRegistrationDraft } from './tenantRegistrationThunks';
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

// Keeps the invite-request draft when reopening the same tenant's dashboard.
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
  (dispatch, getState) => {
    dispatch(patchTenantDashboard({ tab }));
    if (tab === 'registration') {
      prefillRegistrationFromInviteRequest(dispatch, getState());
    }
  };

// Prefills the registration name from the invite request, never overwriting a typed one.
function prefillRegistrationFromInviteRequest(dispatch: AppDispatch, state: RootState): void {
  const request = state.tenantDashboard.data?.myInviteRequest;
  const environmentName = request?.environmentName?.trim();
  if (!environmentName) {
    return;
  }
  const registration = state.tenantDashboard.registration;
  if (!registration.envName.trim()) {
    dispatch(updateRegistrationDraft({ envName: environmentName }));
  }
}

// setReviewFilter reloads so the filter reaches the platform read.
export const setReviewFilter =
  (next: Partial<ReviewFilterState>): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const reviewFilter = { ...getState().tenantDashboard.reviewFilter, ...next };
    dispatch(patchTenantDashboard({ reviewFilter }));
    await dispatch(loadTenantDashboard());
  };

// setReviewStatusFilter narrows locally, so unlike setReviewFilter it does not reload.
export const setReviewStatusFilter =
  (statuses: string[]): AppThunk =>
  (dispatch, getState) => {
    const reviewFilter = { ...getState().tenantDashboard.reviewFilter, statuses };
    dispatch(patchTenantDashboard({ reviewFilter }));
  };

// tenantDashboardWhoamiResolved is false when whoami was never reached, or
// answered for a different platform tenant (tenant-mismatch).
function tenantDashboardWhoamiResolved(platformState: string | undefined): boolean {
  return (
    platformState !== TENANT_PLATFORM_STATE_NOT_CONNECTED &&
    platformState !== TENANT_PLATFORM_STATE_CHOOSE_ALIAS &&
    platformState !== TENANT_PLATFORM_STATE_NOT_SIGNED_IN &&
    platformState !== TENANT_PLATFORM_STATE_TENANT_MISMATCH
  );
}

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
    // forceRefetch because this cache is never invalidated. RTK Query's condition()
    // drops a forced refetch while the same query is pending, so wait or a later
    // caller inherits pre-mutation data.
    await dispatch(tenantApi.util.getRunningQueryThunk('getTenantDashboard', input));
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
      if (tenantDashboardWhoamiResolved(data.platformState)) {
        // This load just called the same whoami the enrollment poll uses.
        dispatch(tenantInviteRequestApi.util.invalidateTags(['TenantEnrollment']));
      }
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

// The chosen alias lives in session state and resets when the dashboard reopens.
export const chooseTenantPlatformAlias =
  (alias: string): AppThunk<Promise<void>> =>
  async (dispatch) => {
    dispatch(patchTenantDashboard({ platformAliasOverride: alias.trim() }));
    await dispatch(loadTenantDashboard());
  };

// The Go side resolves the platform alias; the environment is only for the API-log panel.
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
