import { useAppSelector } from './hooks';
import {
  type DiffReviewStatusSlot,
  emptyDiffReviewStatusSlot,
} from './slices/diffReviewStatusSlice';

// useDiffReviewStatusSlot returns one environment's review-status chip state,
// substituting the empty (still-checking) slot when the environment has not
// been resolved yet -- mirrors useEnvDiffSlot for the same reason: byEnv is
// deliberately sparse, so every consumer would otherwise repeat the fallback.
export function useDiffReviewStatusSlot(envKey: string): DiffReviewStatusSlot {
  return useAppSelector(
    (state) => state.diffReviewStatus.byEnv[envKey] ?? emptyDiffReviewStatusSlot,
  );
}
