import type { UIPlatformContextOutcome, UIPlatformEnvironmentOutcome } from '@/types';

import { tenantApi } from './api/tenantApi';
import { readError } from './errors';
import { showNotification } from './notificationThunks';
import { patchTenantDashboard } from './slices/tenantDashboardSlice';
import type { AppDispatch, AppThunk, RootState } from './store';
import { loadTenantDashboard } from './tenantDialogThunks';
import {
  defaultEnvironmentActionState,
  type EnvironmentActionState,
  type RegistrationState,
} from './tenantRegistrationState';

// updateRegistrationDraft lives in Redux so drafts survive tab switches.
export const updateRegistrationDraft =
  (values: Partial<RegistrationState>): AppThunk =>
  (dispatch, getState) => {
    const current = getState().tenantDashboard.registration;
    dispatch(patchTenantDashboard({ registration: { ...current, ...values } }));
  };

function patchEnvAction(
  dispatch: AppDispatch,
  registration: RegistrationState,
  environmentId: string,
  patch: Partial<EnvironmentActionState>,
): void {
  const current = registration.envActions[environmentId] ?? defaultEnvironmentActionState();
  dispatch(
    patchTenantDashboard({
      registration: {
        ...registration,
        envActions: { ...registration.envActions, [environmentId]: { ...current, ...patch } },
      },
    }),
  );
}

export const createPlatformContext =
  (preview: boolean): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const tenant = getState().tenantDashboard.tenant;
    const draft = getState().tenantDashboard.registration;
    if (!tenant || draft.creatingContext) {
      return;
    }
    dispatch(
      updateRegistrationDraft({
        creatingContext: true,
        createContextError: '',
        createContextConflict: '',
      }),
    );
    try {
      const outcome = await dispatch(
        tenantApi.endpoints.createPlatformContext.initiate({
          tenant,
          name: draft.contextName.trim(),
          cloudProviderAlias: draft.contextCloudProviderAlias.trim(),
          region: draft.contextRegion.trim(),
          instanceType: draft.contextInstanceType.trim() || undefined,
          preview,
        }),
      ).unwrap();
      handleCreateContextOutcome(dispatch, preview, outcome);
      if (outcome.kind === 'accepted' && !preview) {
        await dispatch(loadTenantDashboard());
      }
    } catch (error) {
      dispatch(
        updateRegistrationDraft({ creatingContext: false, createContextError: readError(error) }),
      );
    }
  };

function handleCreateContextOutcome(
  dispatch: AppDispatch,
  preview: boolean,
  outcome: UIPlatformContextOutcome,
): void {
  if (outcome.kind !== 'accepted') {
    dispatch(
      updateRegistrationDraft({
        creatingContext: false,
        createContextConflict: outcome.message ?? '',
      }),
    );
    return;
  }
  if (preview) {
    dispatch(
      updateRegistrationDraft({ creatingContext: false, contextPreviewPlan: outcome.plan ?? [] }),
    );
    return;
  }
  dispatch(
    updateRegistrationDraft({
      creatingContext: false,
      contextName: '',
      contextCloudProviderAlias: '',
      contextRegion: '',
      contextInstanceType: '',
      contextPreviewPlan: null,
    }),
  );
  dispatch(showNotification('success', `Created cloud context ${outcome.context?.name ?? ''}.`));
}

// environmentInputFromDraft is shared by Preview and Register so the previewed
// plan matches the registration; adopt omits contextId/runtimeVersion.
function environmentInputFromDraft(
  tenant: string,
  draft: RegistrationState,
): {
  tenant: string;
  name: string;
  type: string;
  contextId?: string;
  kubernetesContext?: string;
  runtimeVersion?: string;
  adopt?: boolean;
} {
  return {
    tenant,
    name: draft.envName.trim(),
    type: draft.envType,
    contextId: draft.envAdopt ? undefined : draft.envContextId.trim() || undefined,
    kubernetesContext: draft.envKubernetesContext.trim() || undefined,
    runtimeVersion: draft.envAdopt ? undefined : draft.envRuntimeVersion.trim() || undefined,
    adopt: draft.envAdopt || undefined,
  };
}

export const previewPlatformEnvironment =
  (): AppThunk<Promise<void>> => async (dispatch, getState) => {
    const tenant = getState().tenantDashboard.tenant;
    const draft = getState().tenantDashboard.registration;
    if (!tenant || draft.envPreviewing) {
      return;
    }
    dispatch(updateRegistrationDraft({ envPreviewing: true, envPreviewError: '' }));
    try {
      const result = await dispatch(
        tenantApi.endpoints.previewPlatformEnvironment.initiate(
          environmentInputFromDraft(tenant, draft),
        ),
      ).unwrap();
      dispatch(
        updateRegistrationDraft({
          envPreviewing: false,
          envPreviewPlan: result.plan,
          envPreviewQuotaOk: result.quotaOk,
        }),
      );
    } catch (error) {
      dispatch(
        updateRegistrationDraft({ envPreviewing: false, envPreviewError: readError(error) }),
      );
    }
  };

export const registerPlatformEnvironment =
  (): AppThunk<Promise<void>> => async (dispatch, getState) => {
    const tenant = getState().tenantDashboard.tenant;
    const draft = getState().tenantDashboard.registration;
    if (!tenant || draft.envRegistering) {
      return;
    }
    dispatch(
      updateRegistrationDraft({
        envRegistering: true,
        envRegisterError: '',
        envRegisterConflict: '',
      }),
    );
    try {
      const outcome = await dispatch(
        tenantApi.endpoints.registerPlatformEnvironment.initiate(
          environmentInputFromDraft(tenant, draft),
        ),
      ).unwrap();
      handleRegisterOutcome(dispatch, outcome);
      if (outcome.kind === 'accepted') {
        await dispatch(loadTenantDashboard());
      }
    } catch (error) {
      dispatch(
        updateRegistrationDraft({ envRegistering: false, envRegisterError: readError(error) }),
      );
    }
  };

function handleRegisterOutcome(dispatch: AppDispatch, outcome: UIPlatformEnvironmentOutcome): void {
  if (outcome.kind !== 'accepted') {
    dispatch(
      updateRegistrationDraft({
        envRegistering: false,
        envRegisterConflict: outcome.message ?? '',
      }),
    );
    return;
  }
  dispatch(
    updateRegistrationDraft({
      envRegistering: false,
      envName: '',
      envContextId: '',
      envKubernetesContext: '',
      envRuntimeVersion: '',
      envAdopt: false,
      envPreviewPlan: null,
      envPreviewQuotaOk: null,
    }),
  );
  dispatch(
    showNotification(
      'success',
      outcome.environment?.status === 'registered' && !outcome.environment.runtimeVersion
        ? `Registered ${outcome.environment.name}.`
        : `Registered environment ${outcome.environment?.name ?? ''}.`,
    ),
  );
}

// prefillEnvironmentFromLocal seeds an adopt-mode draft from a local environment.
export const prefillEnvironmentFromLocal =
  (local: { name: string; type?: string; kubernetesContext?: string }): AppThunk =>
  (dispatch) => {
    dispatch(
      updateRegistrationDraft({
        envName: local.name,
        envType: local.type ?? 'runtime',
        envContextId: '',
        envKubernetesContext: local.kubernetesContext ?? '',
        envRuntimeVersion: '',
        envAdopt: true,
        envPreviewPlan: null,
        envPreviewQuotaOk: null,
        envPreviewError: '',
        envRegisterError: '',
        envRegisterConflict: '',
      }),
    );
  };

type EnvironmentActionKind = 'deploy' | 'stop' | 'delete';

function environmentActionSuccessMessage(
  kind: EnvironmentActionKind,
  outcome: UIPlatformEnvironmentOutcome,
): string {
  const name = outcome.environment?.name ?? '';
  switch (kind) {
    case 'deploy':
      return `Deploying ${name}.`;
    case 'stop':
      return `Stopped ${name}.`;
    case 'delete':
      return `Deleting ${name}.`;
  }
}

// handleEnvironmentActionOutcome renders conflict/unavailable inline on the row
// as recoverable states, not errors.
async function handleEnvironmentActionOutcome(
  dispatch: AppDispatch,
  getState: () => RootState,
  kind: EnvironmentActionKind,
  environmentId: string,
  outcome: UIPlatformEnvironmentOutcome,
): Promise<void> {
  const registration = getState().tenantDashboard.registration;
  if (outcome.kind === 'conflict') {
    patchEnvAction(dispatch, registration, environmentId, {
      action: '',
      conflictMessage: outcome.message ?? '',
    });
    return;
  }
  if (outcome.kind === 'unavailable') {
    patchEnvAction(dispatch, registration, environmentId, {
      action: '',
      unavailableMessage: outcome.message ?? '',
    });
    return;
  }
  patchEnvAction(dispatch, registration, environmentId, { action: '' });
  dispatch(showNotification('success', environmentActionSuccessMessage(kind, outcome)));
  await dispatch(loadTenantDashboard());
}

export const deployPlatformEnvironment =
  (environmentId: string): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const registration = getState().tenantDashboard.registration;
    if (registration.envActions[environmentId]?.action) {
      return;
    }
    const tenant = getState().tenantDashboard.tenant;
    const version = registration.deployVersionDrafts[environmentId] ?? '';
    patchEnvAction(dispatch, registration, environmentId, {
      action: 'deploy',
      error: '',
      conflictMessage: '',
      unavailableMessage: '',
    });
    try {
      const outcome = await dispatch(
        tenantApi.endpoints.deployPlatformEnvironment.initiate({
          tenant,
          environmentId,
          version: version.trim() || undefined,
        }),
      ).unwrap();
      await handleEnvironmentActionOutcome(dispatch, getState, 'deploy', environmentId, outcome);
    } catch (error) {
      const after = getState().tenantDashboard.registration;
      patchEnvAction(dispatch, after, environmentId, { action: '', error: readError(error) });
    }
  };

export const stopPlatformEnvironment =
  (environmentId: string): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const registration = getState().tenantDashboard.registration;
    if (registration.envActions[environmentId]?.action) {
      return;
    }
    const tenant = getState().tenantDashboard.tenant;
    patchEnvAction(dispatch, registration, environmentId, {
      action: 'stop',
      error: '',
      conflictMessage: '',
      unavailableMessage: '',
    });
    try {
      const outcome = await dispatch(
        tenantApi.endpoints.stopPlatformEnvironment.initiate({ tenant, environmentId }),
      ).unwrap();
      await handleEnvironmentActionOutcome(dispatch, getState, 'stop', environmentId, outcome);
    } catch (error) {
      const after = getState().tenantDashboard.registration;
      patchEnvAction(dispatch, after, environmentId, { action: '', error: readError(error) });
    }
  };

// deletePlatformEnvironment runs only after confirmDeletePlatformEnvironment.
export const deletePlatformEnvironment =
  (environmentId: string): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const registration = getState().tenantDashboard.registration;
    if (registration.envActions[environmentId]?.action) {
      return;
    }
    const tenant = getState().tenantDashboard.tenant;
    dispatch(
      updateRegistrationDraft({ deleteConfirmingEnvironmentId: '', deleteConfirmationDraft: '' }),
    );
    patchEnvAction(dispatch, registration, environmentId, {
      action: 'delete',
      error: '',
      conflictMessage: '',
      unavailableMessage: '',
    });
    try {
      const outcome = await dispatch(
        tenantApi.endpoints.deletePlatformEnvironment.initiate({ tenant, environmentId }),
      ).unwrap();
      await handleEnvironmentActionOutcome(dispatch, getState, 'delete', environmentId, outcome);
    } catch (error) {
      const after = getState().tenantDashboard.registration;
      patchEnvAction(dispatch, after, environmentId, { action: '', error: readError(error) });
    }
  };

export const confirmDeletePlatformEnvironment =
  (environmentId: string): AppThunk =>
  (dispatch) => {
    dispatch(
      updateRegistrationDraft({
        deleteConfirmingEnvironmentId: environmentId,
        deleteConfirmationDraft: '',
      }),
    );
  };

export const cancelDeleteConfirmation = (): AppThunk => (dispatch) => {
  dispatch(
    updateRegistrationDraft({ deleteConfirmingEnvironmentId: '', deleteConfirmationDraft: '' }),
  );
};
