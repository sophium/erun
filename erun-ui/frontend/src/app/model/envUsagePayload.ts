import type { UIRuntimeUsage } from '@/uiRuntimeTypes';

// EnvUsagePayload mirrors the Go-side env-usage payload: a cached reading of
// what this environment's own runtime pod is using, published on the usage
// sweep's cadence (environment_usage.go) rather than per-hover. ObservedAtUnix
// and StaleAfterSeconds are what let a hover card show the reading's age and
// mark it stale, matching UIEnvironmentUsageSnapshot's seeded shape.
export interface EnvUsagePayload {
  tenant: string;
  environment: string;
  usage: UIRuntimeUsage;
  observedAtUnix: number;
  staleAfterSeconds: number;
}
