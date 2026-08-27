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

// UIRuntimeUsage is one live reading of the selected environment's own CPU,
// memory and disk usage against its cgroup limits — as opposed to
// UIRuntimeResourceStatus, which reads the node. `available` is the probe's
// own reachability; cpu/memory/disk each additionally carry their own
// `available`/`unavailable`, since cgroup v1, an unlimited limit, or an
// unreadable file are all normal readings, not probe failures. A field must
// never be read as a real 0 unless `available` is true.
export interface UIRuntimeUsage {
  tenant: string;
  environment: string;
  available: boolean;
  message?: string;
  cpu: UIRuntimeCPUUsage;
  memory: UIRuntimeMemoryUsage;
  disk?: UIRuntimeDiskUsage[];
  warnings?: string[];
}

export interface UIRuntimeCPUUsage {
  available: boolean;
  unavailable?: string;
  quotaCores?: number;
  quota?: string;
  utilizationPercent?: number;
  utilization?: string;
}

// `unlimited` is a real, available reading (no ceiling declared), distinct
// from `unavailable` (the cgroup file could not be read at all).
export interface UIRuntimeMemoryUsage {
  available: boolean;
  unavailable?: string;
  unlimited?: boolean;
  currentBytes?: number;
  current?: string;
  peakBytes?: number;
  peak?: string;
  limitBytes?: number;
  limit?: string;
  percentOfLimit?: number;
  oomKills: number;
}

export interface UIRuntimeDiskUsage {
  mount: string;
  available: boolean;
  unavailable?: string;
  totalBytes?: number;
  total?: string;
  usedBytes?: number;
  used?: string;
  percentUsed?: number;
  percent?: string;
}

// One resource's resolved change, mirroring eruncommon.RuntimeResizeAction.
export interface UIRuntimeSizingAction {
  resource: string;
  from: string;
  to: string;
}

// UIRuntimeSizingRecommendation is the environment's own standing sizing
// recommendation (erun-common/runtime_sizing.go), read via `erun resize
// --apply-recommendation --dry-run` run inside the pod — the recommendation is
// derived from usage history retained there and never leaves it, so the
// desktop cannot compute this host-side the way it computes UIRuntimeUsage.
// `available: false` covers both "nothing to recommend yet" and "could not be
// read", matching UIRuntimeUsage's fail-soft contract.
export interface UIRuntimeSizingRecommendation {
  tenant: string;
  environment: string;
  available: boolean;
  message?: string;
  noOp?: boolean;
  actions?: UIRuntimeSizingAction[];
}
