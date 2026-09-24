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
  // remainingComponents names the platform components the stop deliberately
  // left running. A stop scales the runtime Deployment, not the environment, so
  // the outcome has to say which component pods it left holding capacity —
  // otherwise the pods still standing afterwards read as a stop that failed.
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
