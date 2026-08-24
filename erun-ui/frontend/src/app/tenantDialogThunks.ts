import type { UITenant, UITenantConfig, UITenantDashboardInput } from '@/types';

import { cloudApi } from './api/cloudApi';
import { tenantApi } from './api/tenantApi';
import { replaceCloudProvider } from './cloudContextState';
import { readError } from './errors';
import { showNotification, showTerminalMessage } from './notificationThunks';
import { setIdleStatus } from './slices/idleSlice';
import { setReviewOpen } from './slices/layoutSlice';
import { setSelected } from './slices/selectionSlice';
import { patchTenantDashboard, setTenantDashboard } from './slices/tenantDashboardSlice';
import { patchTenantDialog, setTenantDialog } from './slices/tenantDialogSlice';
import { setCloudProviders, setTenants } from './slices/tenantsSlice';
import { defaultTenantDialog, type TenantDashboardTab, type TenantDialogState } from './state';
import type { AppThunk } from './store';
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
    dispatch(showTerminalMessage(message));
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
      dispatch(showTerminalMessage(message));
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

export const openTenantDashboard =
  (tenant: string): AppThunk =>
  (dispatch, getState) => {
    tenant = tenant.trim();
    if (!tenant) {
      return;
    }
    const currentDashboard = getState().tenantDashboard;
    dispatch(setSelected(null));
    dispatch(setIdleStatus(null));
    dispatch(
      setTenantDashboard({
        tenant,
        tab: currentDashboard.tenant === tenant ? currentDashboard.tab : 'users',
        loading: true,
        error: '',
        data: null,
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

export const loadTenantDashboard =
  (tenant?: string): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const state = getState();
    const target = (tenant ?? state.tenantDashboard.tenant).trim();
    if (!target || state.tenantDashboard.tenant !== target) {
      return;
    }
    const tenantState = state.tenants.tenants.find((candidate) => candidate.name === target);
    const input = tenantDashboardInput(tenantState);
    if (!input) {
      dispatch(
        patchTenantDashboard({
          loading: false,
          error: 'Tenant dashboard requires an API URL and a primary cloud alias.',
          data: null,
        }),
      );
      return;
    }
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

function tenantDashboardInput(tenant: UITenant | undefined): UITenantDashboardInput | null {
  if (!tenant) {
    return null;
  }
  const environment = tenantDashboardEnvironment(tenant);
  const apiUrl = trimOptional(environment?.apiUrl);
  const cloudProviderAlias = trimOptional(tenant.primaryCloudProviderAlias);
  if (!apiUrl || !cloudProviderAlias) {
    return null;
  }
  return {
    tenant: tenant.name,
    environment: trimOptional(environment?.name),
    apiUrl,
    mcpUrl: trimOptional(environment?.mcpUrl),
    kubernetesContext: trimOptional(environment?.kubernetesContext),
    cloudProviderAlias,
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
