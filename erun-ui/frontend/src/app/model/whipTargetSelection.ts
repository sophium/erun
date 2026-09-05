// WhipTargetSelection is the whip control's own working selection: which
// individual environment/orchestrator ids the operator checked, plus a
// per-category "all" mode that tracks the *population*, not a snapshot of ids
// -- so a target that becomes eligible after "Select all X" was clicked is
// still included when the whip actually runs (erun#1700's "group selects
// follow the population, not a snapshot"). Never persisted: a fresh popover
// open always recomputes this from the current sidebar focus, never a
// previous invocation's choice.
export interface WhipTargetSelection {
  environmentMode: 'all' | 'custom';
  orchestratorMode: 'all' | 'custom';
  selectedEnvironmentIds: string[];
  selectedOrchestratorIds: string[];
}

// WhipDefaultTarget is the one target a fresh popover open preselects: the
// sidebar's current focus, translated to the id/name a whip target row uses.
// Null means nothing is focused (dashboard, or nothing at all) -- that must
// never fall back to "select everything" (erun#1700).
export type WhipDefaultTarget =
  | { kind: 'environment'; id: string; name: string }
  | { kind: 'orchestrator'; id: string; name: string }
  | null;
