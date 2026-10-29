// Environment diagnostics read-model types; re-exported from `@/types`.

// label is a legible "service.namespace:port" shown instead of a host.
export interface UIContainerRegistryCluster {
  service: string;
  namespace: string;
  port: number;
  insecure: boolean;
  label: string;
}

export interface UIClusterRegistryStatus {
  deployed: boolean;
  service?: string;
  namespace?: string;
  port?: number;
}

// reason and recovery are set only when available is false.
export interface UIHostedRegistryStatus {
  host: string;
  available: boolean;
  reason?: string;
  recovery?: string;
}

// fix is 'deploy' | 'set-registry', empty when passing.
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
