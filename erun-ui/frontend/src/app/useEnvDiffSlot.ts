import { useAppSelector } from './hooks';
import { emptyEnvDiffState, type EnvDiffState } from './slices/reviewSlice';

// useEnvDiffSlot returns one environment's diff state, substituting the empty
// slot when the environment has not been fetched yet. diffByEnv is deliberately
// sparse, so without this every consumer repeats the same fallbacks -- which is
// both noise and, in ReviewRangeControl, enough branching to blow the module's
// complexity budget (#1178).
export function useEnvDiffSlot(envKey: string): EnvDiffState {
  return useAppSelector((state) => state.review.diffByEnv[envKey] ?? emptyEnvDiffState);
}
