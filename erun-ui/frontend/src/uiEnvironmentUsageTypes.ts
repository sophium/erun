import type { UIRuntimeUsage } from '@/uiRuntimeTypes';

// UIEnvironmentUsageSnapshot mirrors the Go uiEnvironmentUsageSnapshot: the
// environment-usage sweep's last cached reading for this env
// (environment_usage.go), seeded onto the initial state for the same reason
// UIEnvironmentActivity is — a Redux reset that does not restart the Go
// process must not wait out a full sweep interval before a hover card has
// anything to show.
//
// ObservedAtUnix and StaleAfterSeconds travel with every reading rather than
// the reading alone, so a renderer can show the figure's age and mark it
// stale without hardcoding the sweep's own interval — an unlabelled stale
// number reads as a live one, which is worse than showing nothing.
export interface UIEnvironmentUsageSnapshot {
  usage: UIRuntimeUsage;
  observedAtUnix: number;
  staleAfterSeconds: number;
}
