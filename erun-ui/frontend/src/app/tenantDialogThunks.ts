import { cloudApi } from './api/cloudApi';
import { tenantApi } from './api/tenantApi';
import { replaceCloudProvider } from './cloudContextState';
import { readError } from './errors';
import {
  showNotification,
  showTerminalMessage,
} from './notificationThunks';
import {
  defaultTenantDialog,
  type TenantDashboardTab,
  type TenantDialogState,
} from './state';
import type { AppThunk } from './store';
import { requireController } from './thunkExtra';
import type {
  UITenant,
  UITenantConfig,
  UITenantDashboardInput,
} from '@/types';

// tenantDialogThunks own the tenant settings modal and the tenant dashboard
// view (users, queue, builds, audit-log tabs). State mutations route through
// the controller proxy, which dispatches the matching slice actions.

export const openTenantDialog = (tenant: string): AppThunk => (dispatch, _getState, extra) => {
  const controller = requireController(extra);
  controller.state.tenantDialog = {
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
  };
  void dispatch(loadTenantConfig());
};

export const closeTenantDialog = (): AppThunk => (_dispatch, _getState, extra) => {
  const controller = requireController(extra);
  if (controller.state.tenantDialog.busy) {
    return;
  }
  controller.state.tenantDialog = defaultTenantDialog();
  controller.focusTerminalSoon();
};

export const updateTenantDialog = (values: Partial<TenantDialogState>): AppThunk =>
  (_dispatch, _getState, extra) => {
    const controller = requireController(extra);
    if (controller.state.tenantDialog.busy) {
      return;
    }
    controller.state.tenantDialog = {
      ...controller.state.tenantDialog,
      ...values,
      error: values.error ?? '',
    };
  };

export const updateTenantConfig = (values: Partial<UITenantConfig>): AppThunk =>
  (dispatch, _getState, extra) => {
    const controller = requireController(extra);
    if (controller.state.tenantDialog.busy || controller.state.tenantDialog.configLoading) {
      return;
    }
    dispatch(updateTenantDialog({
      config: {
        ...controller.state.tenantDialog.config,
        ...values,
      },
    }));
  };

export const loadTenantConfig = (): AppThunk<Promise<void>> =>
  async (dispatch, _getState, extra) => {
    const controller = requireController(extra);
    const dialog = controller.state.tenantDialog;
    if (!dialog.open || !dialog.tenant) {
      return;
    }
    controller.state.tenantDialog = {
      ...dialog,
      configLoading: true,
      error: '',
    };
    try {
      const result = await dispatch(
        tenantApi.endpoints.getTenantConfig.initiate(dialog.tenant),
      ).unwrap();
      if (result.cloudProviders) {
        controller.state.cloudProviders = result.cloudProviders;
      }
      controller.state.tenantDialog = {
        ...controller.state.tenantDialog,
        config: result,
        configLoading: false,
        error: '',
      };
    } catch (error) {
      controller.state.tenantDialog = {
        ...controller.state.tenantDialog,
        configLoading: false,
        error: readError(error),
      };
    }
  };

export const submitTenantConfig = (): AppThunk<Promise<void>> =>
  async (dispatch, _getState, extra) => {
    const controller = requireController(extra);
    const dialog = controller.state.tenantDialog;
    if (dialog.busy || dialog.configLoading) {
      return;
    }
    if (!dialog.tenant) {
      dispatch(closeTenantDialog());
      return;
    }
    controller.state.tenantDialog = { ...dialog, busy: true, busyAction: 'save', busyTarget: '', error: '' };
    try {
      const result = await dispatch(
        tenantApi.endpoints.saveTenantConfig.initiate(dialog.config),
      ).unwrap();
      applySavedTenantConfig(controller, result);
      controller.state.tenantDialog = {
        ...controller.state.tenantDialog,
        config: result,
        busy: false,
        busyAction: '',
        busyTarget: '',
        error: '',
      };
      dispatch(showNotification('success', `Saved config for ${result.name}.`));
      dispatch(closeTenantDialog());
    } catch (error) {
      const message = readError(error);
      controller.state.tenantDialog = {
        ...controller.state.tenantDialog,
        busy: false,
        busyAction: '',
        busyTarget: '',
        error: message,
      };
      dispatch(showTerminalMessage(message));
    }
  };

export const setupTenantCloudProviderOIDC = (alias: string): AppThunk<Promise<void>> =>
  async (dispatch, _getState, extra) => {
    const controller = requireController(extra);
    alias = alias.trim();
    const dialog = controller.state.tenantDialog;
    if (!alias || dialog.busy || dialog.configLoading) {
      return;
    }
    controller.state.tenantDialog = { ...dialog, busy: true, busyAction: 'cloud-oidc', busyTarget: alias, error: '' };
    try {
      const provider = await dispatch(
        cloudApi.endpoints.setupCloudProviderOIDC.initiate(alias),
      ).unwrap();
      controller.state.cloudProviders = replaceCloudProvider(controller.state.cloudProviders, provider);
      const currentProviders = controller.state.tenantDialog.config.cloudProviders || [];
      controller.state.tenantDialog = {
        ...controller.state.tenantDialog,
        config: {
          ...controller.state.tenantDialog.config,
          cloudProviders: replaceCloudProvider(currentProviders, provider),
        },
        busy: false,
        busyAction: '',
        busyTarget: '',
        error: '',
      };
      dispatch(showNotification('success', `Updated OIDC issuer for ${provider.alias}.`));
    } catch (error) {
      const message = readError(error);
      controller.state.tenantDialog = {
        ...controller.state.tenantDialog,
        busy: false,
        busyAction: '',
        busyTarget: '',
        error: message,
      };
      dispatch(showTerminalMessage(message));
      dispatch(showNotification('error', message));
    }
  };

function applySavedTenantConfig(
  controller: NonNullable<ReturnType<typeof requireController>>,
  config: UITenantConfig,
): void {
  const tenantName = config.name.trim();
  if (!tenantName) {
    return;
  }
  controller.state.tenants = controller.state.tenants.map((tenant) => {
    if (tenant.name !== tenantName) {
      return tenant;
    }
    return {
      ...tenant,
      cloudProviderAliases: config.cloudProviderAliases || [],
      primaryCloudProviderAlias: config.primaryCloudProviderAlias || '',
    };
  });
  if (config.cloudProviders) {
    controller.state.cloudProviders = config.cloudProviders;
  }
}

// Tenant dashboard ============================================================

export const openTenantDashboard = (tenant: string): AppThunk =>
  (dispatch, _getState, extra) => {
    const controller = requireController(extra);
    tenant = tenant.trim();
    if (!tenant) {
      return;
    }
    controller.state.selected = null;
    controller.state.idleStatus = null;
    controller.state.tenantDashboard = {
      tenant,
      tab: controller.state.tenantDashboard.tenant === tenant ? controller.state.tenantDashboard.tab : 'users',
      loading: true,
      error: '',
      data: null,
    };
    controller.state.reviewOpen = false;
    dispatch(showTerminalMessage(''));
    void dispatch(loadTenantDashboard(tenant));
  };

export const setTenantDashboardTab = (tab: TenantDashboardTab): AppThunk =>
  (_dispatch, _getState, extra) => {
    const controller = requireController(extra);
    controller.state.tenantDashboard = {
      ...controller.state.tenantDashboard,
      tab,
    };
  };

export const loadTenantDashboard = (tenant?: string): AppThunk<Promise<void>> =>
  async (dispatch, _getState, extra) => {
    const controller = requireController(extra);
    const target = (tenant ?? controller.state.tenantDashboard.tenant).trim();
    if (!target || controller.state.tenantDashboard.tenant !== target) {
      return;
    }
    const tenantState = controller.state.tenants.find((candidate) => candidate.name === target);
    const input = tenantDashboardInput(tenantState);
    if (!input) {
      controller.state.tenantDashboard = {
        ...controller.state.tenantDashboard,
        loading: false,
        error: 'Tenant dashboard requires an API URL and a primary cloud alias.',
        data: null,
      };
      return;
    }
    controller.state.tenantDashboard = { ...controller.state.tenantDashboard, loading: true, error: '' };
    try {
      const loadedData = await dispatch(
        tenantApi.endpoints.getTenantDashboard.initiate(input),
      ).unwrap();
      if (controller.state.tenantDashboard.tenant !== target) {
        return;
      }
      const data = { ...loadedData, environment: loadedData.environment || input.environment };
      controller.state.tenantDashboard = {
        ...controller.state.tenantDashboard,
        loading: false,
        error: '',
        data,
      };
    } catch (error) {
      if (controller.state.tenantDashboard.tenant !== target) {
        return;
      }
      controller.state.tenantDashboard = {
        ...controller.state.tenantDashboard,
        loading: false,
        error: readError(error),
        data: null,
      };
    }
  };

export const refreshTenantDashboard = (): AppThunk<Promise<void>> =>
  async (dispatch, _getState, extra) => {
    const controller = requireController(extra);
    const tenant = controller.state.tenantDashboard.tenant;
    await dispatch(loadTenantDashboard(tenant));
    if (controller.state.tenantDashboard.tenant === tenant && !controller.state.tenantDashboard.error) {
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

function tenantDashboardEnvironment(tenant: UITenant): UITenant['environments'][number] | undefined {
  const defaultEnvironment = tenant.defaultEnvironment?.trim();
  return tenant.environments.find((candidate) => candidate.name === defaultEnvironment && candidate.apiUrl) ||
    tenant.environments.find((candidate) => candidate.apiUrl);
}

function trimOptional(value: string | undefined): string {
  return value?.trim() || '';
}
