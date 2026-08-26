// UI read-model types for the Ports tab's public-exposure surface (issue
// #1351). Configured/Restricted keep the three empty states distinct: not
// applicable to this environment, you may not see this, and genuinely empty.
export interface UIExposureList {
  configured: boolean;
  restricted: boolean;
  error?: string;
  services: UIExposedService[];
}

export interface UIExposedService {
  service: string;
  hostname: string;
  scheme: string;
}

export interface UIExposeServiceInput {
  service: string;
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
  targetIP: string;
  // Free-text so the field can be empty (falls back to the default port
  // server-side) without fighting a number input's own coercion; parsed to a
  // number only at submit time.
  port: string;
}
