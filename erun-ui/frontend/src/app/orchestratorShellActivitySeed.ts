import type { OrchestratorInfo } from './slices/orchestratorsSlice';

// planOrchestratorShellSeed seeds shell activity from the fetched list, as
// planOrchestratorBusySeed does for busy.
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
