import type { UIAccessRemedy } from '@/types';

// accessRemedyFor looks a denial's remedy up by the restricted route the
// denial is about. The backend keys them by that same route, so a denial that
// has one always finds it and one that does not renders exactly as it did
// before this hand-over existed.
export function accessRemedyFor(
  remedies: Record<string, UIAccessRemedy> | undefined,
  restricted: string | undefined,
): UIAccessRemedy | undefined {
  if (!remedies || !restricted) {
    return undefined;
  }
  return remedies[restricted];
}
