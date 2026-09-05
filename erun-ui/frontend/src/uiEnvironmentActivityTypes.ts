// UIEnvironmentActivity mirrors the Go uiEnvironmentActivitySnapshot: the
// environment-activity poller's last observation for this env, seeded onto
// the initial state so a Redux reset that does not restart the Go process
// (the ErrorBoundary "Reload app" button) does not have to wait for the next
// poll transition to learn a still-busy env — the poller only re-emits its
// event on a transition, and a long-running env may never produce one.
// Split out of `@/types` to keep that file under its line budget; re-exported
// from there so consumers keep one import surface.
export interface UIEnvironmentActivity {
  reachable: boolean;
  observed: boolean;
  outage: boolean;
  // checkFailed is outage's counterpart for an environment with no local
  // forward: a real attempt to ask it (over the environment's own runtime
  // pod, not this desktop's connection) that did not come back, as opposed to
  // an environment nobody has asked about at all.
  checkFailed?: boolean;
  busy: boolean;
  detail?: string;
}
