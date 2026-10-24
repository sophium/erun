import type { UITenant } from '@/types';
import type { UIEnvironmentNodeSnapshot } from '@/uiEnvironmentNodeTypes';

import { selectionKey } from './versionSuggestions';

// planEnvNodeSeed seeds node readings after a Redux reset, since the env-node
// event fires only on change. Envs without a node are omitted: an absent key
// means "no node erun manages".
export function planEnvNodeSeed(
  tenants: UITenant[],
): { key: string; node: UIEnvironmentNodeSnapshot }[] {
  const seed: { key: string; node: UIEnvironmentNodeSnapshot }[] = [];
  for (const tenant of tenants) {
    for (const environment of tenant.environments) {
      if (!environment.node) {
        continue;
      }
      seed.push({
        key: selectionKey({ tenant: tenant.name, environment: environment.name }),
        node: environment.node,
      });
    }
  }
  return seed;
}
