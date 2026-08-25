import type { UITenant } from '@/types';

import { tenantApi } from './api/tenantApi';
import { readError } from './errors';
import { showNotification } from './notificationThunks';
import { patchMergeQueueAction } from './slices/mergeQueueActionSlice';
import type { AppThunk } from './store';

// mergeQueueCallerContext resolves the API URL and cloud alias the write
// needs, split out so submitAdvanceMergeQueue's own branching stays under
// the module's complexity cap.
function mergeQueueCallerContext(
  tenant: string,
  apiUrl: string,
  targetBranch: string,
  tenants: UITenant[],
): { apiUrl: string; cloudProviderAlias: string } | null {
  const trimmedApiUrl = apiUrl.trim();
  const cloudProviderAlias = tenants
    .find((candidate) => candidate.name === tenant)
    ?.primaryCloudProviderAlias?.trim();
  if (!trimmedApiUrl || !cloudProviderAlias || !targetBranch) {
    return null;
  }
  return { apiUrl: trimmedApiUrl, cloudProviderAlias };
}

// mergeQueueThunks drives the Merge Queue panel's "Advance queue" action: an
// inline confirm step (cancel-before-commitment), then the write itself. The
// target branch is derived from the queue's own rows rather than typed by the
// operator — see mergeQueueTargetBranch in TenantDashboardPanels.tsx.

export const confirmAdvanceMergeQueue = (): AppThunk => (dispatch) => {
  dispatch(patchMergeQueueAction({ confirming: true, error: '' }));
};

export const cancelAdvanceMergeQueue = (): AppThunk => (dispatch) => {
  dispatch(patchMergeQueueAction({ confirming: false, error: '' }));
};

export const submitAdvanceMergeQueue =
  (targetBranch: string): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const state = getState();
    const { tenant, data } = state.tenantDashboard;
    const context = mergeQueueCallerContext(
      tenant,
      data?.apiUrl ?? '',
      targetBranch,
      state.tenants.tenants,
    );
    if (!context) {
      dispatch(
        patchMergeQueueAction({
          error: 'Advancing the queue requires an API URL and a primary cloud alias.',
        }),
      );
      return;
    }
    dispatch(patchMergeQueueAction({ busy: true, error: '' }));
    try {
      const review = await dispatch(
        tenantApi.endpoints.advanceMergeQueue.initiate({
          tenant,
          apiUrl: context.apiUrl,
          cloudProviderAlias: context.cloudProviderAlias,
          targetBranch,
        }),
      ).unwrap();
      dispatch(patchMergeQueueAction({ busy: false, confirming: false, error: '' }));
      dispatch(
        showNotification('success', `Advanced ${review.name || review.reviewId} to merged.`),
      );
    } catch (error) {
      dispatch(patchMergeQueueAction({ busy: false, error: readError(error) }));
    }
  };
