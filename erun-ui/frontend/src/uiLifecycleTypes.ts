// Results the environment stop and delete actions report.

export interface UIEnvironmentStopResult {
  tenant: string;
  environment: string;
  release?: string;
  namespace?: string;
  alreadyStopped: boolean;
  // A stop scales only the runtime, so components still running are named
  // rather than read as a failed stop.
  remainingComponents?: string[];
}

export interface DeleteEnvironmentResult {
  tenant: string;
  environment: string;
  namespace?: string;
  kubernetesContext?: string;
  namespaceDeleteError?: string;
  cloudContextStopError?: string;
}
