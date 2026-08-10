// EnvActivityPayload mirrors the Go-side env-activity payload. reachable is
// true whenever the environment's MCP edge answers — whoever opened it, the
// desktop or a bare `erun open` — so a CLI-driven env is no longer blank. busy
// says the environment reports work in flight, and detail names it. outage is
// the diagnosis for an environment that had a port-forward and no longer has a
// working one — the local port free, or held by something that replies to
// nothing — after re-establishing it failed to help.
export interface EnvActivityPayload {
  tenant: string;
  environment: string;
  reachable: boolean;
  observed: boolean;
  outage?: boolean;
  busy: boolean;
  detail?: string;
}
