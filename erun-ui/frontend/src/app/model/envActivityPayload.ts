// EnvActivityPayload mirrors the Go-side env-activity payload. reachable is
// true whenever the environment's MCP edge answers — whoever opened it, the
// desktop or a bare `erun open` — so a CLI-driven env is no longer blank. busy
// says the environment reports work in flight, and detail names it. stale is
// the diagnosis for a forward that holds its local port while nothing replies
// through it, after re-establishing it failed to help.
export interface EnvActivityPayload {
  tenant: string;
  environment: string;
  reachable: boolean;
  observed: boolean;
  stale?: boolean;
  busy: boolean;
  detail?: string;
}
