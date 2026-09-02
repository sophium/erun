// UI read-model types for the Ports tab's public-exposure surface (issue
// #1351). Configured/Restricted keep the three empty states distinct: not
// applicable to this environment, you may not see this, and genuinely empty.
export interface UIExposureList {
  configured: boolean;
  restricted: boolean;
  error?: string;
  services: UIExposedService[];
  // notConfiguredReason distinguishes the two reasons configured can be
  // false, which call for different copy and different recovery: a host
  // environment has no cluster and can never be exposed
  // ("host-environment"), while a cluster-backed environment whose project
  // has no platform: block yet just hasn't been set up for it
  // ("no-platform-block", the fixable case). Empty when configured is true.
  // Bare string to match the Wails binding, which widens the Go string
  // constant; narrow against UIExposureNotConfiguredReason at the read site.
  notConfiguredReason?: string;
}

export type UIExposureNotConfiguredReason = 'host-environment' | 'no-platform-block';

export interface UIExposedService {
  service: string;
  hostname: string;
  scheme: string;
  // The in-namespace Service the Ingress routes to, read from the Ingress
  // rather than derived from `service`: the two differ whenever the chart that
  // rendered the Service is the repo's own.
  backendService?: string;
}

// UIEnvironmentServiceList is what the environment is actually running — the
// list the picker offers. Same three empty states as UIExposureList, on
// purpose: both reads fail for the same reasons and the tab renders them the
// same way.
export interface UIEnvironmentServiceList {
  configured: boolean;
  restricted: boolean;
  error?: string;
  services: UIEnvironmentService[];
  notConfiguredReason?: string;
}

export interface UIEnvironmentService {
  name: string;
  type?: string;
  ports?: UIEnvironmentServicePort[];
  // Set when this Service already has a public hostname. exposedAs is the
  // label in that hostname, which is not necessarily the Service's own name.
  hostname?: string;
  scheme?: string;
  exposedAs?: string;
}

export interface UIEnvironmentServicePort {
  name?: string;
  port: number;
}

export interface UIExposeServiceInput {
  service: string;
  // The Service in the namespace to route to. The picker fills it in from the
  // operator's choice; empty keeps the <tenant>-<service> derivation.
  backendService?: string;
  targetIP: string;
  port?: number;
}

export interface UIUnexposeResult {
  wildcardName: string;
}

// ExposeServiceFormState is the Ports tab's "Expose a service" form, dialog-
// owned like the rest of ManageDialogState.
export interface ExposeServiceFormState {
  service: string;
  // backendService is the Service picked from the environment; the public
  // label defaults to it (trimmed of the tenant prefix) but stays editable,
  // since the label is what appears in the hostname.
  backendService: string;
  targetIP: string;
  // Free-text so the field can be empty (falls back to the default port
  // server-side) without fighting a number input's own coercion; parsed to a
  // number only at submit time.
  port: string;
}
