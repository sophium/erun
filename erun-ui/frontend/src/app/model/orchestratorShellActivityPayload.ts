// OrchestratorShellActivityPayload is independent of AI busy: a background shell
// can outlive its turn. StartedAtUnix is meaningful only while running.
export interface OrchestratorShellActivityPayload {
  sessionId: number;
  running: boolean;
  command: string;
  startedAtUnix: number;
}
