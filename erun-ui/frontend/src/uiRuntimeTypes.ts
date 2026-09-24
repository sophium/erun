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
  // excludesBuilds marks a runtime pod carrying the erun-dind sidecar builds
  // actually run in: cpu and memory above are the runtime container's alone and
  // can never see a build. Mirrors eruncommon.RuntimeUsage.ExcludesBuilds.
  excludesBuilds?: boolean;
  // dind is that sidecar's own reading, and is what makes a build-capable
  // environment's CPU legible — see UIRuntimeDindUsage. Absent on every other
  // environment, and absent (not zeroed) when the exec into the sidecar failed.
  dind?: UIRuntimeDindUsage;
  // requests is what the scheduler admits this pod on, read from the pod spec.
  // cpu and memory above are measured against the container's cgroup *limit* --
  // a ceiling that reserves nothing -- so a renderer states the reservation
  // beside it rather than letting the ceiling read as provisioned. Absent when
  // nothing was read; `unavailable` (never a zero) when the pod spec could not
  // be.
  requests?: UIRuntimeUsageRequests;
}

// UIRuntimeUsageRequests mirrors eruncommon.RuntimeUsageRequests' runtime
// container half: the reservation the cpu and memory rows above are measured
// against.
export interface UIRuntimeUsageRequests {
  runtime: UIRuntimeContainerRequests;
  unavailable?: string;
}

// UIRuntimeContainerRequests is one container's declared request. A zero/empty
// field is a container that declares nothing for that resource — which reserves
// nothing — and must render as absent rather than as a measured zero.
export interface UIRuntimeContainerRequests {
  cpuMilli?: number;
  memoryBytes?: number;
  cpu?: string;
  memory?: string;
}

// UIRuntimeDindUsage is the erun-dind sidecar's own CPU/memory reading. It
// carries no disk field deliberately: the sidecar mounts the same workspace
// volume UIRuntimeUsage.disk already reports, and a second figure under its own
// name invites reading it as an independent filesystem.
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
  // usageUsec is cpu.stat's cumulative usage for the current container
  // lifetime, and travels on an unavailable reading too: utilisation needs a
  // ceiling to be a fraction of, and the sidecar is commonly declared with no
  // cpu.max quota, so this is the only CPU figure a build environment can offer
  // in that case.
  usageUsec?: number;
}

// UIRuntimeRunState is what an environment's runtime Deployment currently
// reports — the state the Runtime tab shows before Stop is pressed, because
// Stop is only a real action when the Deployment wants pods. `present` is
// deliberately separate from `stopped`: an environment that was never deployed
// and one scaled to zero are different facts with different recoveries, and
// neither is `message` — a failed read is a third state, and rendering it as
// "not deployed" would state a fact nobody observed.
export interface UIRuntimeRunState {
  tenant: string;
  environment: string;
  present: boolean;
  desiredReplicas: number;
  readyReplicas: number;
  stopped: boolean;
  // remainingComponents names the platform components a stop of this
  // environment would leave running, so the control can say what it will not
  // touch before it is pressed. Empty for a runtime-only environment.
  remainingComponents?: string[];
  message?: string;
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
  // verdicts and evidence are the reasoning behind actions/noOp -- one prose
  // line per resource, then the window/sample count the recommendation was
  // computed from. Present whenever the environment has a standing
  // recommendation, even when it resolves to noOp, so a bare "already sized"
  // is never a dead end.
  verdicts?: string[];
  evidence?: string;
}
