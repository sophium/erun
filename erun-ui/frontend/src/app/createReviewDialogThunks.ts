import type { UITenant } from '@/types';

import { EnvironmentWorkingIssue } from '../../wailsjs/go/main/App';
import { execApi } from './api/execApi';
import { tenantApi } from './api/tenantApi';
import { readError } from './errors';
import { showNotification } from './notificationThunks';
import { openReviewDetail } from './reviewDetailThunks';
import { patchCreateReviewDialog, resetCreateReviewDialog } from './slices/createReviewDialogSlice';
import type { AppThunk } from './store';

// createReviewDialogThunks drives the "Open a review" dialog: pushing the
// selected environment's branch (commit, then push — the precondition
// CreateReview's sourceBranch needs) and, once pushed, creating the review.
// Each write is its own explicit button with its own busy/error state, so
// none of this fires implicitly on open (Professional UX: side-effecting
// actions need a visible boundary, not an on-open side effect).

function dialogCallerContext(
  tenant: string,
  environment: string,
  apiUrl: string,
  tenants: UITenant[],
): { apiUrl: string; cloudProviderAlias: string } | null {
  const cloudProviderAlias = tenants
    .find((candidate) => candidate.name === tenant)
    ?.primaryCloudProviderAlias?.trim();
  if (!apiUrl.trim() || !cloudProviderAlias || !tenant.trim() || !environment.trim()) {
    return null;
  }
  return { apiUrl: apiUrl.trim(), cloudProviderAlias };
}

export const openCreateReviewDialog = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const state = getState();
  const { tenant, data } = state.tenantDashboard;
  const environment = data?.environment?.trim() ?? '';
  dispatch(
    patchCreateReviewDialog({
      ...resetCreateReviewDialogState(),
      open: true,
      tenant,
      environment,
      apiUrl: data?.apiUrl?.trim() ?? '',
      branchLoading: Boolean(environment),
    }),
  );
  if (!environment) {
    return;
  }
  await dispatch(loadCreateReviewDialogBranch(tenant, environment));
};

// loadCreateReviewDialogBranch reads the environment's current branch to
// prefill sourceBranch — split out of openCreateReviewDialog so that
// function's own branching stays under the module's complexity cap.
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

// resetCreateReviewDialogState is a plain value (not a dispatched action) so
// openCreateReviewDialog can spread it into one patch alongside `open: true`.
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

// commitCreateReviewBranch commits every change in the selected environment's
// working tree — an explicit, separate step from push so the operator can
// commit without pushing, or push a branch that is already committed.
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

// pushCreateReviewBranch pushes the selected environment's current branch —
// CreateReview's own precondition, since the platform can only reference a
// sourceBranch that already exists on the remote.
export const pushCreateReviewBranch = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const dialog = getState().createReviewDialog;
  if (!dialog.sourceBranch || dialog.pushing) {
    return;
  }
  // Starting the push clears the commit step's failure too: it belongs to an
  // attempt the operator has moved past, and leaving it renders a red banner
  // beside the green "Pushed to origin/…" badge, which says two contradictory
  // things about the same branch.
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

// clearCreateReviewError drops a stale create-review write error once a
// sign-in it prompted has succeeded, so the dialog recovers instead of
// showing the identical error and button next to a now-valid session
// (#1392) — the operator retries Create themselves from there.
export const clearCreateReviewError = (): AppThunk => (dispatch) => {
  dispatch(patchCreateReviewDialog({ createError: '' }));
};

export const submitCreateReview = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const state = getState();
  const dialog = state.createReviewDialog;
  const name = dialog.name.trim();
  const targetBranch = dialog.targetBranch.trim();
  const sourceBranch = (dialog.pushedBranch || dialog.sourceBranch).trim();
  if (!name || !targetBranch || !sourceBranch || dialog.creating) {
    return;
  }
  const context = dialogCallerContext(
    dialog.tenant,
    dialog.environment,
    dialog.apiUrl,
    state.tenants.tenants,
  );
  if (!context) {
    dispatch(
      patchCreateReviewDialog({
        createError: 'Opening a review requires an API URL and a primary cloud alias.',
      }),
    );
    return;
  }
  dispatch(patchCreateReviewDialog({ creating: true, createError: '' }));
  try {
    const review = await dispatch(
      tenantApi.endpoints.createReview.initiate({
        tenant: dialog.tenant,
        apiUrl: context.apiUrl,
        cloudProviderAlias: context.cloudProviderAlias,
        name,
        targetBranch,
        sourceBranch,
      }),
    ).unwrap();
    dispatch(resetCreateReviewDialog());
    dispatch(showNotification('success', `Opened ${review.name || review.reviewId}.`));
    void dispatch(openReviewDetail(review.reviewId));
  } catch (error) {
    dispatch(patchCreateReviewDialog({ creating: false, createError: readError(error) }));
  }
};
