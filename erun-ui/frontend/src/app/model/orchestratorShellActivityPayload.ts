// OrchestratorShellActivityPayload drives the sidebar's "shell running"
// indicator: a background shell an orchestrator started can outlive
// the turn that started it, so this is independent of AIActivityPayload's
// busy signal, not a variant of it. StartedAtUnix is only meaningful while
// running is true.
export interface OrchestratorShellActivityPayload {
  sessionId: number;
  running: boolean;
  command: string;
  startedAtUnix: number;
}
