import type { UITenant } from '@/types';

import type { EnvObservedActivity } from './slices/envStatusSlice';
import { selectionKey } from './versionSuggestions';

// planEnvActivitySeed derives the activityByEnv entries a freshly fetched
// tenant list implies (erun#1216). The environment-activity poller only
// re-emits its Wails event on a transition, so a Redux reset that does not
// restart the Go process (the ErrorBoundary "Reload app" button) had nothing
// to seed a still-busy env's row from until its next transition — which for
// a long-running env may never come. uiEnvironment now carries the poller's
// last observation directly, so a fetch alone reproduces the correct seed
// with no event required, mirroring planOrchestratorBusySeed.
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
