import { DiffReviewStatus, EnvironmentWorkingIssue } from '../../wailsjs/go/main/App';
import { readError } from './errors';
import { selectReviewTargets } from './selectors';
import {
  pruneDiffReviewStatuses,
  setDiffReviewStatus,
  setDiffReviewStatusError,
  setDiffReviewStatusLoading,
} from './slices/diffReviewStatusSlice';
import type { AppThunk } from './store';

// loadDiffReviewStatus renders an unknown branch as "unavailable", never as a
// confirmed "no review".
export const loadDiffReviewStatus =
  (
    envKey: string,
    tenant: string,
    environment: string,
    targetBranch: string,
  ): AppThunk<Promise<void>> =>
  async (dispatch) => {
    const target = targetBranch.trim();
    if (!tenant.trim() || !environment.trim() || !target) {
      return;
    }
    dispatch(setDiffReviewStatusLoading({ envKey }));
    try {
      const issue = await EnvironmentWorkingIssue({ tenant, environment });
      const sourceBranch = issue.available ? (issue.branch ?? '').trim() : '';
      if (!sourceBranch) {
        dispatch(
          setDiffReviewStatus({
            envKey,
            status: { state: 'unavailable', canAdvanceMergeQueue: false },
          }),
        );
        return;
      }
      const status = await DiffReviewStatus({ tenant, sourceBranch, targetBranch: target });
      dispatch(setDiffReviewStatus({ envKey, status }));
    } catch (error) {
      dispatch(setDiffReviewStatusError({ envKey, error: readError(error) }));
    }
  };

export const pruneStaleDiffReviewStatuses = (): AppThunk => (dispatch, getState) => {
  dispatch(pruneDiffReviewStatuses(selectReviewTargets(getState()).map((target) => target.envKey)));
};
