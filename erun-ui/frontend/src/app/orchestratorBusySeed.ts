import type { OrchestratorInfo } from './slices/orchestratorsSlice';

// planOrchestratorBusySeed seeds busy from the fetched list so a row need not have
// witnessed the ai-activity event. Stopped orchestrators have no session key.
export function planOrchestratorBusySeed(
  items: OrchestratorInfo[],
): { sessionId: number; busy: boolean }[] {
  return items
    .filter((item) => item.sessionId > 0)
    .map((item) => ({ sessionId: item.sessionId, busy: item.busy }));
}
