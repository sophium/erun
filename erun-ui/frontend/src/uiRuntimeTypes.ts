// Runtime read-model types: one live reading of what an environment's runtime
// pod is running, and the reclaim contract for its build leftovers. Kept beside
// @/types rather than in it so the shared type file stays within its size
// budget, following the uiDiagnosticsTypes / uiLifecycleTypes split.

// UIRuntimeActivity is one live reading of what an environment's runtime pod is
// running: its persistent sessions and the processes holding memory.
export interface UIRuntimeActivity {
  tenant: string;
  environment: string;
  available: boolean;
  message?: string;
  sessionsRunning: number;
  sessions?: UIRuntimeSession[];
  processes?: UIRuntimeProcessGroup[];
  memoryHeld?: string;
  memoryHeldMiB?: number;
}

// `running` is observed in the pod (a live program behind the session socket),
// never inferred from how recently the session printed something.
export interface UIRuntimeSession {
  id: string;
  running: boolean;
  program?: string;
}

// A class of resource-holding process the operator can recognise and, when
// `reclaim` is set, act on.
export interface UIRuntimeProcessGroup {
  id: string;
  label: string;
  count: number;
  memory: string;
  memoryMiB: number;
  reclaim?: string;
  reclaimLabel?: string;
}

export interface UIRuntimeReclaimInput {
  tenant: string;
  environment: string;
  action: string;
}

export interface UIRuntimeReclaimResult {
  action: string;
  message: string;
}
