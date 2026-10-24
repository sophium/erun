// Seeded onto initial state: the poller only emits on transitions, so a Redux
// reset without a Go restart would otherwise miss a still-busy env.
export interface UIEnvironmentActivity {
  reachable: boolean;
  observed: boolean;
  outage: boolean;
  // A real check that did not come back, as opposed to one never made.
  checkFailed?: boolean;
  busy: boolean;
  detail?: string;
}
