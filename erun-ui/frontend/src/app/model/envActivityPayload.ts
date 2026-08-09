// EnvActivityPayload mirrors the Go-side env-activity payload. reachable is
// true whenever the environment's MCP edge answers — whoever opened it, the
// desktop or a bare `erun open` — so a CLI-driven env is no longer blank. busy
// says the environment reports work in flight, and detail names it.
export interface EnvActivityPayload {
  tenant: string;
  environment: string;
  reachable: boolean;
  observed: boolean;
  busy: boolean;
  detail?: string;
}
