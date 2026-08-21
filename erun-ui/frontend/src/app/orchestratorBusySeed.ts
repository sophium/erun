import type { OrchestratorInfo } from './slices/orchestratorsSlice';

// planOrchestratorBusySeed derives the aiBusyBySession entries a freshly
// fetched orchestrator list implies (#1087). It is the frontend half of the
// snapshot fix: orchestratorInfo.busy now carries the same signal as the
// ai-activity event, so loadOrchestrators can seed the event-keyed store
// directly from the list it already fetched, rather than requiring the row to
// have witnessed the event that last changed the state. Only orchestrators
// with a live session are considered — a stopped one has no session id to key
// the store by.
export function planOrchestratorBusySeed(
  items: OrchestratorInfo[],
): { sessionId: number; busy: boolean }[] {
  return items
    .filter((item) => item.sessionId > 0)
    .map((item) => ({ sessionId: item.sessionId, busy: item.busy }));
}
