import { DiffReviewStatus, EnvironmentWorkingIssue } from '../../wailsjs/go/main/App';
import { readError } from './errors';
import { selectReviewEnvTargets } from './selectors';
import {
  pruneDiffReviewStatuses,
  setDiffReviewStatus,
  setDiffReviewStatusError,
  setDiffReviewStatusLoading,
} from './slices/diffReviewStatusSlice';
import type { AppThunk } from './store';

// diffReviewStatusThunks resolves the diff panel's review-status chip: reads
// the environment's current branch (EnvironmentWorkingIssue -- the same read
// createReviewDialogThunks already uses to prefill the "Open a review"
// dialog's sourceBranch), then asks the platform where that branch's pair
// with targetBranch sits on the review ladder (DiffReviewStatus), reusing the
// exact reads the Reviews tab and merge queue already make rather than a
// parallel query path.

// loadDiffReviewStatus resolves one environment section's chip. It is a
// background enrichment read, not a user action, so a precondition gap (no
// branch known yet) renders as the honest "unavailable" state rather than an
// error -- the chip must never present that gap as a confirmed "no review".
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

// pruneStaleDiffReviewStatuses drops chip state for environments no longer
// shown, mirroring pruneEnvDiffs in reviewThunks.ts's loadReviewDiff.
export const pruneStaleDiffReviewStatuses = (): AppThunk => (dispatch, getState) => {
  dispatch(
    pruneDiffReviewStatuses(selectReviewEnvTargets(getState()).map((target) => target.envKey)),
  );
};
