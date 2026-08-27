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
  busy: boolean;
  detail?: string;
  // AI* fields mirror the env's primary AI session's own structured report
  // (erun-common's AISessionStatus, reduced by reduceAISessionStatus) — never
  // inferred from PTY output volume or timing. aiState is a plain string
  // (not a union) because it structurally mirrors the Wails-generated
  // uiEnvironmentActivitySnapshot, which cannot express a Go string const's
  // literal values; it is one of 'busy' | 'idle' | 'awaiting-input' |
  // 'unknown' in practice, or absent when the env has no AI session running
  // at all (distinct from 'unknown', a session that exists but has never
  // self-reported). aiOutcome is 'exited' | 'oom-killed' once the tool
  // process has ended.
  aiState?: string;
  aiTool?: string;
  aiLastActivityUnix?: number;
  aiOutcome?: string;
  aiExitCode?: number;
}
