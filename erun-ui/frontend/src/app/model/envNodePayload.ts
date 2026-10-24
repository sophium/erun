import type { UIEnvironmentNodeSnapshot } from '@/uiEnvironmentNodeTypes';

// EnvNodePayload: an absent node means no erun-managed node backs the env; a
// failed or pending read arrives as status '' or 'unknown'.
export interface EnvNodePayload {
  tenant: string;
  environment: string;
  node?: UIEnvironmentNodeSnapshot;
}
