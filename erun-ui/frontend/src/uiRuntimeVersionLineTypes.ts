// UIRuntimeVersionLine mirrors eruncommon.RuntimeVersionLine: which release
// line an environment's runtimeVersion number belongs to -- erun's own, or a
// tenant's own <tenant>-devops line. See erun-common/runtime_version_line.go.
export interface UIRuntimeVersionLine {
  line?: string;
  image?: string;
  undetermined?: boolean;
  disagrees?: boolean;
}

// UIErunVersion mirrors eruncommon.ErunVersion: the erun version an
// environment's runtime chart carries, which can differ from runtimeVersion
// whenever the runtime image itself rides a tenant's own release line.
export interface UIErunVersion {
  version?: string;
  sameAsRuntimeVersion?: boolean;
}

// UIRuntimeImageLineMismatch mirrors eruncommon.RuntimeImageLineMismatchResult:
// present only when the environment's recorded and last-observed runtime
// images name different release lines.
export interface UIRuntimeImageLineMismatch {
  recordedLine: string;
  observedLine: string;
}
