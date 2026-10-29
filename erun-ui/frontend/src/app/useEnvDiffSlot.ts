import { useAppSelector } from './hooks';
import { emptyEnvDiffState, type EnvDiffState } from './slices/reviewSlice';

// diffByEnv is sparse; this substitutes the empty slot for an unfetched environment.
export function useEnvDiffSlot(envKey: string): EnvDiffState {
  return useAppSelector((state) => state.review.diffByEnv[envKey] ?? emptyEnvDiffState);
}
