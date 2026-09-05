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

// environmentInputFromDraft builds the one field set Preview and Register
// both submit, so the plan an operator previews can never diverge from what
// register then does: adopt forbids contextId/runtimeVersion, matching the
// platform's own adopt validation, so it never sends fields the current
// draft's mode does not use.
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

// previewPlatformEnvironment resolves the full ordered plan (quota,
// placement, namespace, register, deploy — or, for an adopt draft, the
// no-deploy adopt plan) for the drafted environment without creating
// anything — always a preview, shown before the register action that
// follows it, over the exact fields register would submit.
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

// registerPlatformEnvironment registers a hosted environment, or — for an
// adopt draft — records one that already exists without deploying
// anything. A quota-cap refusal renders as envRegisterConflict, the
// recoverable state (5): the operator's next action is deleting or stopping
// another environment, not retrying blindly.
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

// prefillEnvironmentFromLocal loads the register-on-the-row affordance's
// draft: the desktop already knows a local environment's name, type and
// kubernetes context (RegistrationEnvironmentsSection's local-environments
// list), so putting it on the platform starts from those values instead of
// a blank form, in adopt mode with nothing to deploy.
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
