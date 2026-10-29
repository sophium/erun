import type { UIRuntimeUsage } from '@/uiRuntimeTypes';

// EnvUsagePayload is a cached reading published on the usage sweep, not per hover.
export interface EnvUsagePayload {
  tenant: string;
  environment: string;
  usage: UIRuntimeUsage;
  observedAtUnix: number;
  staleAfterSeconds: number;
}
