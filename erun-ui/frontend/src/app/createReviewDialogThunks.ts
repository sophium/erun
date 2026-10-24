import type { UIReviewCreateCapability } from '@/types';

import { EnvironmentWorkingIssue, TenantReviewCreateCapability } from '../../wailsjs/go/main/App';
import { execApi } from './api/execApi';
import { tenantApi } from './api/tenantApi';
import { loadDiffReviewStatus } from './diffReviewStatusThunks';
import { readError } from './errors';
import { showNotification } from './notificationThunks';
import { openReviewDetail } from './reviewDetailThunks';
import { patchCreateReviewDialog, resetCreateReviewDialog } from './slices/createReviewDialogSlice';
import type { AppThunk, RootState } from './store';

// Drives the "Open a review" dialog. Commit, push and create are explicit
// buttons; nothing fires on open.

// CreateReviewDialogContext is what the caller already knows. knownCapability
// skips the dialog's own capability probe, which can disagree with the
// dashboard's current answer.
export interface CreateReviewDialogContext {
  tenant?: string;
  environment?: string;
  targetBranch?: string;
  knownCapability?: boolean;
}

function resolveCreateReviewDialogContext(
  context: CreateReviewDialogContext | undefined,
  state: RootState,
): { tenant: string; environment: string; targetBranch: string } {
  const tenant = (context?.tenant ?? state.tenantDashboard.tenant).trim();
  const environment = (
    context?.environment ??
    state.tenantDashboard.data?.environment ??
    ''
  ).trim();
  return { tenant, environment, targetBranch: normalizeTargetBranch(context?.targetBranch) };
}

// Not `hint || 'main'`: that trips prefer-nullish-coalescing.
function normalizeTargetBranch(hint: string | undefined): string {
  const trimmed = hint?.trim();
  if (!trimmed) {
    return 'main';
  }
  return trimmed;
}

export const openCreateReviewDialog =
  (context?: CreateReviewDialogContext): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const { tenant, environment, targetBranch } = resolveCreateReviewDialogContext(
      context,
      getState(),
    );
    const knownCapability = context?.knownCapability ?? false;
    dispatch(
      patchCreateReviewDialog({
        ...resetCreateReviewDialogState(),
        open: true,
        tenant,
        environment,
        targetBranch,
        branchLoading: Boolean(environment),
        capabilityLoading: !knownCapability && Boolean(tenant),
      }),
    );
    await Promise.all([
      knownCapability || !tenant
        ? Promise.resolve()
        : dispatch(loadCreateReviewDialogCapability(tenant)),
      environment ? dispatch(loadCreateReviewDialogBranch(tenant, environment)) : Promise.resolve(),
    ]);
  };

// The diff panel can open the dialog before the dashboard loads, so the
// dialog resolves its own capability.
const loadCreateReviewDialogCapability =
  (tenant: string): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    try {
      // Typed to the hand-written shape, as wailsQueryFn does.
      const capability = (await TenantReviewCreateCapability(tenant)) as UIReviewCreateCapability;
      const dialog = getState().createReviewDialog;
      if (!dialog.open || dialog.tenant !== tenant) {
        return;
      }
      dispatch(
        patchCreateReviewDialog({
          capabilityLoading: false,
          capabilityRestricted: capability.canCreate ? '' : (capability.restricted ?? ''),
          capabilityRemedy: capability.canCreate ? undefined : capability.accessRemedy,
        }),
      );
    } catch (error) {
      const dialog = getState().createReviewDialog;
      if (!dialog.open || dialog.tenant !== tenant) {
        return;
      }
      dispatch(
        patchCreateReviewDialog({ capabilityLoading: false, capabilityError: readError(error) }),
      );
    }
  };

const loadCreateReviewDialogBranch =
  (tenant: string, environment: string): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    try {
      const issue = await EnvironmentWorkingIssue({ tenant, environment });
      if (!getState().createReviewDialog.open) {
        return;
      }
      dispatch(
        patchCreateReviewDialog({
          branchLoading: false,
          sourceBranch: issue.available ? issue.branch : '',
          branchError: issue.available ? '' : (issue.reason ?? 'The current branch is not known.'),
        }),
      );
    } catch (error) {
      dispatch(patchCreateReviewDialog({ branchLoading: false, branchError: readError(error) }));
    }
  };

// A plain value so it can be spread into one patch with `open: true`.
function resetCreateReviewDialogState() {
  return {
    name: '',
    targetBranch: 'main',
    sourceBranch: '',
    branchLoading: false,
    branchError: '',
    commitMessage: '',
    committing: false,
    commitError: '',
    pushedBranch: '',
    pushing: false,
    pushError: '',
    creating: false,
    createError: '',
    capabilityLoading: false,
    capabilityRestricted: '',
    capabilityError: '',
  };
}

export const closeCreateReviewDialog = (): AppThunk => (dispatch, getState) => {
  if (getState().createReviewDialog.committing || getState().createReviewDialog.pushing) {
    return;
  }
  dispatch(resetCreateReviewDialog());
};

export const updateCreateReviewDialog =
  (values: { name?: string; targetBranch?: string; commitMessage?: string }): AppThunk =>
  (dispatch) => {
    dispatch(patchCreateReviewDialog(values));
  };

// commitCreateReviewBranch commits the environment's whole working tree.
export const commitCreateReviewBranch =
  (): AppThunk<Promise<void>> => async (dispatch, getState) => {
    const dialog = getState().createReviewDialog;
    const message = dialog.commitMessage.trim();
    if (!dialog.sourceBranch || !message || dialog.committing) {
      return;
    }
    dispatch(patchCreateReviewDialog({ committing: true, commitError: '' }));
    try {
      await dispatch(
        execApi.endpoints.execCommit.initiate({
          selection: { tenant: dialog.tenant, environment: dialog.environment },
          input: { branch: dialog.sourceBranch, message },
        }),
      ).unwrap();
      dispatch(patchCreateReviewDialog({ committing: false, commitError: '' }));
    } catch (error) {
      dispatch(patchCreateReviewDialog({ committing: false, commitError: readError(error) }));
    }
  };

// pushCreateReviewBranch pushes the current branch; CreateReview needs it on the remote.
export const pushCreateReviewBranch = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const dialog = getState().createReviewDialog;
  if (!dialog.sourceBranch || dialog.pushing) {
    return;
  }
  // Also clear the stale commit error so it does not contradict the push badge.
  dispatch(patchCreateReviewDialog({ pushing: true, pushError: '', commitError: '' }));
  try {
    const result = await dispatch(
      execApi.endpoints.execPush.initiate({
        selection: { tenant: dialog.tenant, environment: dialog.environment },
        input: { branch: dialog.sourceBranch },
      }),
    ).unwrap();
    dispatch(
      patchCreateReviewDialog({ pushing: false, pushError: '', pushedBranch: result.branch }),
    );
  } catch (error) {
    dispatch(patchCreateReviewDialog({ pushing: false, pushError: readError(error) }));
  }
};

// clearCreateReviewError drops a write error once the sign-in it prompted succeeds.
export const clearCreateReviewError = (): AppThunk => (dispatch) => {
  dispatch(patchCreateReviewDialog({ createError: '' }));
};

export const submitCreateReview = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const state = getState();
  const dialog = state.createReviewDialog;
  const name = dialog.name.trim();
  const targetBranch = dialog.targetBranch.trim();
  const sourceBranch = (dialog.pushedBranch || dialog.sourceBranch).trim();
  if (!name || !targetBranch || !sourceBranch || dialog.creating || !dialog.tenant.trim()) {
    return;
  }
  dispatch(patchCreateReviewDialog({ creating: true, createError: '' }));
  try {
    const review = await dispatch(
      tenantApi.endpoints.createReview.initiate({
        tenant: dialog.tenant,
        name,
        targetBranch,
        sourceBranch,
        // The environment may be a remote worktree this host cannot read, so there is
        // no origin to derive a repository from.
        repository: '',
      }),
    ).unwrap();
    dispatch(resetCreateReviewDialog());
    dispatch(showNotification('success', `Opened ${review.name || review.reviewId}.`));
    void dispatch(openReviewDetail(review.reviewId, dialog.tenant));
    if (dialog.environment) {
      void dispatch(
        loadDiffReviewStatus(
          `${dialog.tenant}/${dialog.environment}`,
          dialog.tenant,
          dialog.environment,
          targetBranch,
        ),
      );
    }
  } catch (error) {
    dispatch(patchCreateReviewDialog({ creating: false, createError: readError(error) }));
  }
};
