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

// `running` is observed in the pod, never inferred from recent output.
export interface UIRuntimeSession {
  id: string;
  running: boolean;
  program?: string;
}

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

// Each resource carries its own availability (cgroup v1, no limit, unreadable
// are normal); never read a field as 0 unless `available` is true.
export interface UIRuntimeUsage {
  tenant: string;
  environment: string;
  available: boolean;
  message?: string;
  cpu: UIRuntimeCPUUsage;
  memory: UIRuntimeMemoryUsage;
  disk?: UIRuntimeDiskUsage[];
  warnings?: string[];
  // cpu/memory are the runtime container's alone and never see a build.
  excludesBuilds?: boolean;
  // Absent, not zeroed, when the sidecar exec failed.
  dind?: UIRuntimeDindUsage;
  // cpu/memory are measured against the limit, which reserves nothing, so the
  // reservation is shown beside it.
  requests?: UIRuntimeUsageRequests;
}

export interface UIRuntimeUsageRequests {
  runtime: UIRuntimeContainerRequests;
  unavailable?: string;
}

// A zero field declares nothing and renders as absent, not a measured zero.
export interface UIRuntimeContainerRequests {
  cpuMilli?: number;
  memoryBytes?: number;
  cpu?: string;
  memory?: string;
}

// No disk field: the sidecar shares the workspace volume UIRuntimeUsage reports.
export interface UIRuntimeDindUsage {
  cpu: UIRuntimeCPUUsage;
  memory: UIRuntimeMemoryUsage;
}

export interface UIRuntimeCPUUsage {
  available: boolean;
  unavailable?: string;
  quotaCores?: number;
  quota?: string;
  utilizationPercent?: number;
  utilization?: string;
  // Present even when unavailable: with no cpu.max quota it is the only CPU figure.
  usageUsec?: number;
}

// `present` and `stopped` are separate (never deployed vs scaled to zero), and
// a failed read is a third state carried by `message`.
export interface UIRuntimeRunState {
  tenant: string;
  environment: string;
  present: boolean;
  desiredReplicas: number;
  readyReplicas: number;
  stopped: boolean;
  remainingComponents?: string[];
  message?: string;
}

// `unlimited` is a real reading, distinct from `unavailable`.
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

export interface UIRuntimeSizingAction {
  resource: string;
  from: string;
  to: string;
}

// Computed in the pod from history that never leaves it.
// `available: false` covers both "nothing yet" and "could not be read".
export interface UIRuntimeSizingRecommendation {
  tenant: string;
  environment: string;
  available: boolean;
  message?: string;
  noOp?: boolean;
  actions?: UIRuntimeSizingAction[];
  verdicts?: string[];
  evidence?: string;
}
