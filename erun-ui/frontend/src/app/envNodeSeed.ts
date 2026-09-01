import type { UITenant } from '@/types';
import type { UIEnvironmentNodeSnapshot } from '@/uiEnvironmentNodeTypes';

import { selectionKey } from './versionSuggestions';

// planEnvNodeSeed is the node counterpart to planEnvActivitySeed: the env-node
// event only fires when a node's reading changes, so a Redux reset that does
// not restart the Go process (the ErrorBoundary "Reload app" button, a page
// reload) has no transition to replay and would render every row as if no node
// had ever been observed. uiEnvironment carries the poller's last reading
// directly, so a fetch alone reproduces the correct seed.
//
// An environment with no node is deliberately absent from the seed rather than
// seeded as undefined: the slice treats an absent key as "no node erun
// manages", which is the same answer.
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
