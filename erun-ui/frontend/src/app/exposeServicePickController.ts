import type { ExposeServiceFormState, UIEnvironmentService } from '@/uiExposureTypes';

// exposeServicePickController turns "the operator picked this Service" into the
// form patch that follows from it. Pure and separate from the component so the
// derivation is testable without rendering the Ports tab.
//
// Two fields are derived rather than typed, because typing either is how the
// current form goes wrong:
//
// - backendService is the Service the Ingress must route to. It used to be
//   derived from the public label as `<tenant>-<label>`, which is only right
//   for a chart erun scaffolded; a repo's own chart names its Service itself,
//   and routing to a name that does not exist yields a hostname that resolves
//   and an ingress that 503s.
// - the public label is seeded from the Service name with the tenant prefix
//   removed, so the conventional case still produces the clean hostname it
//   always did (`frs-api` -> `api.frs-dev...`) while a repo-native name is
//   offered whole. It stays editable: the label is what appears in the URL.
export function exposeFormPatchForService(
  service: UIEnvironmentService,
  tenant: string,
): Partial<ExposeServiceFormState> {
  return {
    backendService: service.name,
    service: publicLabelForService(service.name, tenant),
    port: soleServicePort(service),
  };
}

// publicLabelForService strips a leading `<tenant>-` so the hostname label
// stays the logical one. A Service that merely starts with the tenant's letters
// (`frsapi` for tenant `frs`) is left alone: the separator is what makes it a
// prefix rather than a coincidence.
export function publicLabelForService(serviceName: string, tenant: string): string {
  const prefix = `${tenant.trim()}-`;
  if (
    tenant.trim() !== '' &&
    serviceName.startsWith(prefix) &&
    serviceName.length > prefix.length
  ) {
    return serviceName.slice(prefix.length);
  }
  return serviceName;
}

// soleServicePort fills the port only when there is no choice to make. With
// several ports the field is cleared rather than guessed: picking the first
// would silently expose the wrong one (a metrics port fronted as the app is a
// real way to leak internals), and an empty field falls back to the documented
// default server-side.
export function soleServicePort(service: UIEnvironmentService): string {
  const ports = service.ports ?? [];
  const only = ports.length === 1 ? ports[0] : undefined;
  if (!only) {
    return '';
  }
  return String(only.port);
}

// describeServicePorts renders a Service's ports for the picker row. Named
// ports carry their name because that is how a chart's own values refer to
// them, and an operator choosing between `http` and `metrics` is choosing by
// name, not by number.
export function describeServicePorts(service: UIEnvironmentService): string {
  const ports = service.ports ?? [];
  if (ports.length === 0) {
    return 'no ports';
  }
  return ports
    .map((port) => (port.name ? `${port.name}:${String(port.port)}` : String(port.port)))
    .join(', ');
}
