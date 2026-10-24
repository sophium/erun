import type { ExposeServiceFormState, UIEnvironmentService } from '@/uiExposureTypes';

// exposeFormPatchForService routes the Ingress to the picked Service by its own
// name: a repo's chart names its Service itself, so a derived name would 503.
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

// publicLabelForService strips a leading `<tenant>-`; the separator is required.
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

// soleServicePort fills the port only when there is exactly one, so a metrics
// port is never exposed by guessing.
export function soleServicePort(service: UIEnvironmentService): string {
  const ports = service.ports ?? [];
  const only = ports.length === 1 ? ports[0] : undefined;
  if (!only) {
    return '';
  }
  return String(only.port);
}

export function describeServicePorts(service: UIEnvironmentService): string {
  const ports = service.ports ?? [];
  if (ports.length === 0) {
    return 'no ports';
  }
  return ports
    .map((port) => (port.name ? `${port.name}:${String(port.port)}` : String(port.port)))
    .join(', ');
}
