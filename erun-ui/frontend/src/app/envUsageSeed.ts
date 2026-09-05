import type { UITenant } from '@/types';
import type { UIEnvironmentUsageSnapshot } from '@/uiEnvironmentUsageTypes';

import { selectionKey } from './versionSuggestions';

// planEnvUsageSeed is the usage counterpart to planEnvActivitySeed: the usage
// sweep only publishes its Wails event on its own tick, so a Redux reset that
// does not restart the Go process (the ErrorBoundary "Reload app" button)
// would otherwise have nothing to seed a hover card's cached reading from
// until the next sweep. uiEnvironment carries the sweep's last reading
// directly, so a fetch alone reproduces the correct seed with no event
// required.
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
