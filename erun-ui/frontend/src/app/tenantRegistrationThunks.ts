// tenantRegistrationThunks drives the Registration tab: creating a cloud
// context, previewing hosted-environment provisioning, and registering /
// deploying / stopping / deleting a hosted environment. Split out of
// tenantDialogThunks.ts (mirroring tenantPlatformConnectThunks.ts's split)
// to keep that file under eslint's 500-line cap.

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

// updateRegistrationDraft patches any subset of the Registration tab's form
// drafts. Kept in Redux (not local component state) so switching to another
// dashboard tab and back never loses an in-progress value (Nielsen #3),
// matching connectApiUrlDraft/enrollUsernameDraft's existing precedent.
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

// createPlatformContext registers a cloud context, or — with preview set —
// only resolves its bootstrap plan (rule #3: a register action is preceded
// by a preview, not skipped past). A quota/placement conflict renders as
// createContextConflict, a recoverable state, never createContextError.
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

// previewProvision resolves the full ordered plan (quota, placement,
// namespace, register, deploy) for the drafted environment without creating
// anything — always a preview, shown before RegisterForm's own submit.
export const previewProvision = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const tenant = getState().tenantDashboard.tenant;
  const draft = getState().tenantDashboard.registration;
  if (!tenant || draft.previewing) {
    return;
  }
  dispatch(updateRegistrationDraft({ previewing: true, previewError: '' }));
  try {
    const result = await dispatch(
      tenantApi.endpoints.previewPlatformProvision.initiate({
        tenant,
        envName: draft.previewEnvName.trim(),
        envType: draft.previewEnvType,
        kubernetesContext: draft.previewKubernetesContext.trim() || undefined,
      }),
    ).unwrap();
    dispatch(
      updateRegistrationDraft({
        previewing: false,
        previewPlan: result.plan,
        previewQuotaOk: result.quotaOk,
      }),
    );
  } catch (error) {
    dispatch(updateRegistrationDraft({ previewing: false, previewError: readError(error) }));
  }
};

// registerPlatformEnvironment registers a hosted environment. A quota-cap
// refusal renders as registerConflict, the recoverable state (5): the
// operator's next action is deleting or stopping another environment, not
// retrying blindly.
export const registerPlatformEnvironment =
  (): AppThunk<Promise<void>> => async (dispatch, getState) => {
    const tenant = getState().tenantDashboard.tenant;
    const draft = getState().tenantDashboard.registration;
    if (!tenant || draft.registering) {
      return;
    }
    dispatch(
      updateRegistrationDraft({ registering: true, registerError: '', registerConflict: '' }),
    );
    try {
      const outcome = await dispatch(
        tenantApi.endpoints.registerPlatformEnvironment.initiate({
          tenant,
          name: draft.registerName.trim(),
          type: draft.registerType,
          contextId: draft.registerContextId.trim() || undefined,
          kubernetesContext: draft.registerKubernetesContext.trim() || undefined,
          runtimeVersion: draft.registerRuntimeVersion.trim() || undefined,
        }),
      ).unwrap();
      handleRegisterOutcome(dispatch, outcome);
      if (outcome.kind === 'accepted') {
        await dispatch(loadTenantDashboard());
      }
    } catch (error) {
      dispatch(updateRegistrationDraft({ registering: false, registerError: readError(error) }));
    }
  };

function handleRegisterOutcome(dispatch: AppDispatch, outcome: UIPlatformEnvironmentOutcome): void {
  if (outcome.kind !== 'accepted') {
    dispatch(
      updateRegistrationDraft({ registering: false, registerConflict: outcome.message ?? '' }),
    );
    return;
  }
  dispatch(
    updateRegistrationDraft({
      registering: false,
      registerName: '',
      registerContextId: '',
      registerKubernetesContext: '',
      registerRuntimeVersion: '',
    }),
  );
  dispatch(
    showNotification('success', `Registered environment ${outcome.environment?.name ?? ''}.`),
  );
}

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

// handleEnvironmentActionOutcome is Deploy/Stop/Delete's shared outcome
// classification: "conflict" (a quota cap, or another deploy/delete already
// in flight) and "unavailable" (no deploy executor configured) render
// inline on the row as recoverable states, never as raw errors — mirroring
// the console's DeployOutcome/feedbackRole pattern.
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

// deletePlatformEnvironment performs the actual delete — call it only after
// confirmDeletePlatformEnvironment's confirmation step, the same cancel-
// before-commitment boundary every other destructive dashboard action gets.
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

// confirmDeletePlatformEnvironment/cancelDeleteConfirmation back the
// cancel-before-commitment step every other destructive dashboard action
// gets (root AGENTS.md Design-Language Decision Record).
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
