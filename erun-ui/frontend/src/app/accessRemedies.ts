import type { UIAccessRemedy } from '@/types';

// accessRemedyFor looks up the remedy by the denied route, as the backend keys it.
export function accessRemedyFor(
  remedies: Record<string, UIAccessRemedy> | undefined,
  restricted: string | undefined,
): UIAccessRemedy | undefined {
  if (!remedies || !restricted) {
    return undefined;
  }
  return remedies[restricted];
}
