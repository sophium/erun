// capabilityAllows mirrors erun-common's PlatformCapabilities.Allows exactly
// (Go: erun-common/platform_capabilities.go) — a canonical route-template
// match, one wildcard segment per `{param}` — so a caller can check either a
// literal template (an action gate, not tied to one row's id) or a concrete
// path (a real invite_request_id in place of `{invite_request_id}`) against
// the same capability list. `capabilities` undefined means the platform
// could not resolve a set at all; treat that as unknown, not denied — see
// whoamiApi.ts's Whoami.capabilities doc and erun-ui/AGENTS.md's "Degrade by
// permission" (the server's own per-request check remains the only
// authority regardless of what this renders).
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
