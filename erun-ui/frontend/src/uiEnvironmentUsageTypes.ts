import type { UIRuntimeUsage } from '@/uiRuntimeTypes';

// Seeded onto initial state like UIEnvironmentActivity. The age fields let a
// renderer mark stale readings without hardcoding the sweep interval.
export interface UIEnvironmentUsageSnapshot {
  usage: UIRuntimeUsage;
  observedAtUnix: number;
  staleAfterSeconds: number;
}
