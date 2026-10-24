export interface UIRuntimeVersionLine {
  line?: string;
  image?: string;
  undetermined?: boolean;
  disagrees?: boolean;
}

// Can differ from runtimeVersion when the image rides a tenant's own line.
export interface UIErunVersion {
  version?: string;
  sameAsRuntimeVersion?: boolean;
}

export interface UIRuntimeImageLineMismatch {
  recordedLine: string;
  observedLine: string;
}
