// UI read-model types for environment diagnostics: the in-cluster registry
// block carried on a container-registry entry, the new-environment dialog's
// cluster-registry probe, and the Manage dialog's out-of-pod health check.
// Re-exported from `@/types` so consumers keep one import surface.

// UIContainerRegistryCluster describes a context-resolved in-cluster registry
// (a `cluster:` entry). Label is a legible "service.namespace:port" the editor
// renders instead of an empty host field.
export interface UIContainerRegistryCluster {
  service: string;
  namespace: string;
  port: number;
  insecure: boolean;
  label: string;
}

// UIClusterRegistryStatus reports whether the selected Kubernetes context has an
// in-cluster erun-registry deployed, for the new-environment dialog's default.
export interface UIClusterRegistryStatus {
  deployed: boolean;
  service?: string;
  namespace?: string;
  port?: number;
}

// UIEnvironmentHealthCheck is one out-of-pod diagnostic result from the Manage
// dialog's "Check environment" action. status is 'ok' | 'error' | 'unknown';
// fix names the recovery action ('deploy' | 'set-registry'), empty when passing.
export interface UIEnvironmentHealthCheck {
  id: string;
  status: string;
  title: string;
  detail: string;
  fix?: string;
}

export interface UIEnvironmentHealth {
  tenant: string;
  environment: string;
  healthy: boolean;
  checks: UIEnvironmentHealthCheck[];
}
