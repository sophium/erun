import type { OrchestratorInfo } from './slices/orchestratorsSlice';

// planOrchestratorShellSeed derives the shell-activity-by-session entries a
// freshly fetched orchestrator list implies (#1068), the same snapshot
// treatment planOrchestratorBusySeed applies to the busy signal: the
// orchestratorInfo shell fields carry the same signal as the
// orchestrator-shell-activity event, so loadOrchestrators can seed the
// event-keyed store directly from the list it already fetched rather than
// requiring the row to have witnessed the event that last changed the state.
// Only orchestrators with a live session are considered — a stopped one has
// no session id to key the store by.
export function planOrchestratorShellSeed(
  items: OrchestratorInfo[],
): { sessionId: number; running: boolean; command: string; startedAtUnix: number }[] {
  return items
    .filter((item) => item.sessionId > 0)
    .map((item) => ({
      sessionId: item.sessionId,
      running: item.shellRunning,
      command: item.shellCommand,
      startedAtUnix: item.shellStartedAtUnix,
    }));
}
