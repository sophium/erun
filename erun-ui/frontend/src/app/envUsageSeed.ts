import type { UITenant } from '@/types';
import type { UIEnvironmentUsageSnapshot } from '@/uiEnvironmentUsageTypes';

import { selectionKey } from './versionSuggestions';

// planEnvUsageSeed seeds cached usage after a Redux reset, since the usage
// event fires only on the sweep's tick.
export function planEnvUsageSeed(
  tenants: UITenant[],
): { key: string; usage: UIEnvironmentUsageSnapshot }[] {
  const seed: { key: string; usage: UIEnvironmentUsageSnapshot }[] = [];
  for (const tenant of tenants) {
    for (const environment of tenant.environments) {
      if (!environment.usage) {
        continue;
      }
      seed.push({
        key: selectionKey({ tenant: tenant.name, environment: environment.name }),
        usage: environment.usage,
      });
    }
  }
  return seed;
}
