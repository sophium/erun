// Configured/Restricted keep three empty states distinct: not applicable,
// not permitted, and genuinely empty.
export interface UIExposureList {
  configured: boolean;
  restricted: boolean;
  error?: string;
  services: UIExposedService[];
  // 'host-environment' (never exposable) or 'no-platform-block' (fixable).
  // Bare string: the Wails binding widens it; narrow at the read site.
  notConfiguredReason?: string;
}

export type UIExposureNotConfiguredReason = 'host-environment' | 'no-platform-block';

export interface UIExposedService {
  service: string;
  hostname: string;
  scheme: string;
  // Read from the Ingress; differs from `service` when the repo's chart renders it.
  backendService?: string;
  // cert-manager writes the tls block before issuance, so only the Certificate's
  // Ready condition says the URL serves a valid cert.
  tlsReady?: boolean;
  tlsNotReadyReason?: string;
}

// Same three empty states as UIExposureList.
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
  // exposedAs is the hostname label, not necessarily the Service name.
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
  // Empty keeps the <tenant>-<service> derivation.
  backendService?: string;
  targetIP: string;
  port?: number;
}

export interface UIUnexposeResult {
  wildcardName: string;
}

export interface ExposeServiceFormState {
  service: string;
  backendService: string;
  targetIP: string;
  // Free text so it can be empty (server default) without number-input coercion.
  port: string;
}
