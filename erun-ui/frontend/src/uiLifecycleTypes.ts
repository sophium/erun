// Environment lifecycle result types — what the stop and delete actions report
// back. Kept out of ./types (the shared UI contract surface) the same way the
// diagnostics read-models are, so each file stays within its size budget and
// the lifecycle contracts read together.

// UIEnvironmentStopResult is what stopping an environment reports back.
// alreadyStopped tells the no-op apart from the run that actually returned the
// runtime's and its dind sidecar's capacity to the node.
export interface UIEnvironmentStopResult {
  tenant: string;
  environment: string;
  release?: string;
  namespace?: string;
  alreadyStopped: boolean;
}

export interface DeleteEnvironmentResult {
  tenant: string;
  environment: string;
  namespace?: string;
  kubernetesContext?: string;
  namespaceDeleteError?: string;
  cloudContextStopError?: string;
}
