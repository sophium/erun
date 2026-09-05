import type { UIEnvironmentNodeSnapshot } from '@/uiEnvironmentNodeTypes';

// EnvNodePayload mirrors the Go-side env-node payload: which cloud node backs
// this environment and what power state it was last observed in, republished
// only when that reading changes (erun-ui/environment_node.go).
//
// An absent `node` is the definite answer "no node erun manages backs this
// environment", not a failed read — a read that failed or has not happened yet
// arrives as a node whose status is '' or 'unknown'.
export interface EnvNodePayload {
  tenant: string;
  environment: string;
  node?: UIEnvironmentNodeSnapshot;
}
