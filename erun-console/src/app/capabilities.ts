// capabilityAllows mirrors erun-common's PlatformCapabilities.Allows. Undefined
// capabilities means unknown, not denied; the server's check stays authoritative.
import type { PlatformCapability } from './api/whoamiApi';

function pathMatchesTemplate(path: string, template: string): boolean {
  const pathSegments = path.split('/');
  const templateSegments = template.split('/');
  if (pathSegments.length !== templateSegments.length) {
    return false;
  }
  return templateSegments.every(
    (segment, index) =>
      (segment.startsWith('{') && segment.endsWith('}')) || segment === pathSegments[index],
  );
}

export function capabilityAllows(
  capabilities: PlatformCapability[] | undefined,
  method: string,
  path: string,
): boolean {
  if (capabilities === undefined) {
    return false;
  }
  return capabilities.some(
    (capability) => capability.method === method && pathMatchesTemplate(path, capability.path),
  );
}
