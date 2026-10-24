import type { UITenant } from '@/types';

import type { EnvObservedActivity } from './slices/envStatusSlice';
import { selectionKey } from './versionSuggestions';

// planEnvActivitySeed seeds activity after a Redux reset, since the activity
// event fires only on transitions.
export function planEnvActivitySeed(
  tenants: UITenant[],
): { key: string; activity: EnvObservedActivity }[] {
  const seed: { key: string; activity: EnvObservedActivity }[] = [];
  for (const tenant of tenants) {
    for (const environment of tenant.environments) {
      if (!environment.activity) {
        continue;
      }
      seed.push({
        key: selectionKey({ tenant: tenant.name, environment: environment.name }),
        activity: {
          ...environment.activity,
          detail: environment.activity.detail ?? '',
          checkFailed: environment.activity.checkFailed === true,
        },
      });
    }
  }
  return seed;
}
