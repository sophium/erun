import { useAppSelector } from './hooks';
import {
  type DiffReviewStatusSlot,
  emptyDiffReviewStatusSlot,
} from './slices/diffReviewStatusSlice';

// byEnv is sparse; this substitutes the empty slot for an unresolved environment.
export function useDiffReviewStatusSlot(envKey: string): DiffReviewStatusSlot {
  return useAppSelector(
    (state) => state.diffReviewStatus.byEnv[envKey] ?? emptyDiffReviewStatusSlot,
  );
}
