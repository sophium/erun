// WhipTargetSelection's "all" mode tracks the population, so targets that become
// eligible later are included. Never persisted.
export interface WhipTargetSelection {
  environmentMode: 'all' | 'custom';
  orchestratorMode: 'all' | 'custom';
  selectedEnvironmentIds: string[];
  selectedOrchestratorIds: string[];
}

// WhipDefaultTarget is null when nothing is focused; never fall back to all.
export type WhipDefaultTarget =
  | { kind: 'environment'; id: string; name: string }
  | { kind: 'orchestrator'; id: string; name: string }
  | null;
