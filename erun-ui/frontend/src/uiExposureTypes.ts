// UI read-model types for the Ports tab's public-exposure surface (issue
// #1351; redesigned around discovering real Services rather than a free-text
// field in issue #1906). Configured/Restricted keep the three empty states
// distinct: not applicable to this environment, you may not see this, and
// genuinely empty.
export interface UIExposureList {
  configured: boolean;
  restricted: boolean;
  error?: string;
  services: UIEnvironmentService[];
  // notConfiguredReason distinguishes the two reasons configured can be
  // false, which call for different copy and different recovery: a host
  // environment has no cluster and can never be exposed
  // ("host-environment"), while a cluster-backed environment whose project
  // has no platform: block yet just hasn't been set up for it
  // ("no-platform-block", the fixable case). Empty when configured is true.
  // Bare string to match the Wails binding, which widens the Go string
  // constant; narrow against UIExposureNotConfiguredReason at the read site.
  notConfiguredReason?: string;
  // defaultTargetIP prefills the expose form's Target IP field for a local
  // cluster (127.0.0.1, the VM-backed local case erun expose's docs name).
  // Absent for a remote/cloud env, where there is no safe value to guess.
  defaultTargetIP?: string;
}

export type UIExposureNotConfiguredReason = 'host-environment' | 'no-platform-block';

// UIEnvironmentService is one Service the environment is actually running.
// exposed is ground truth read back from the namespace's own Ingresses,
// never guessed from name. exposableLabel is set only when exposed is false
// and the Service's name follows the tenant's naming convention -- absent in
// every other case, including when exposed is true, so a caller can tell
// "already reachable", "can be exposed as this label", and "erun expose
// cannot route to this Service" apart without inferring one state from
// another.
export interface UIEnvironmentService {
  name: string;
  ports: UIEnvironmentServicePort[];
  exposed: boolean;
  hostname?: string;
  scheme?: string;
  exposableLabel?: string;
}

export interface UIEnvironmentServicePort {
  name?: string;
  port: number;
  protocol?: string;
}

export interface UIExposeServiceInput {
  service: string;
  targetIP: string;
  port?: number;
}

// UIExposePreview is the resolved plan for UIExposeServiceInput before it
// commits anything.
export interface UIExposePreview {
  hostname: string;
  scheme: string;
  tlsEnabled: boolean;
  tlsDisabledReason?: string;
}

export interface UIExposeServiceResult {
  hostname: string;
  scheme: string;
}

export interface UIUnexposeResult {
  wildcardName: string;
}

// ExposeServiceFormState is the Ports tab's "Expose a service" form, dialog-
// owned like the rest of ManageDialogState. selectedService is the real
// Service name chosen from the picker; service is the exposableLabel derived
// from it, which is what actually gets submitted -- kept separate so submit
// never has to re-derive it from the services list. preview/previewLoading/
// previewError track the resolved-hostname preview a pick gets before it
// commits (issue #1906); colocated with the form's own fields rather than
// flat on ManageDialogState since they are derived from, and only ever
// relevant alongside, this same form.
export interface ExposeServiceFormState {
  selectedService: string;
  service: string;
  targetIP: string;
  // Free-text so the field can be empty (falls back to the default port
  // server-side) without fighting a number input's own coercion; parsed to a
  // number only at submit time.
  port: string;
  preview: UIExposePreview | null;
  previewLoading: boolean;
  previewError: string;
}

export function defaultExposeServiceFormState(): ExposeServiceFormState {
  return {
    selectedService: '',
    service: '',
    targetIP: '',
    port: '',
    preview: null,
    previewLoading: false,
    previewError: '',
  };
}
