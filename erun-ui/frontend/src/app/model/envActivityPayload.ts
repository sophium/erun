// EnvActivityPayload: reachable whenever the env's MCP edge answers, whoever
// opened it; outage diagnoses a port-forward that could not be re-established.
export interface EnvActivityPayload {
  tenant: string;
  environment: string;
  reachable: boolean;
  observed: boolean;
  outage?: boolean;
  checkFailed?: boolean;
  busy: boolean;
  detail?: string;
}
