// AIActivityPayload carries an orchestrator's own turn-busy self-report to its
// sidebar row spinner. An environment's AI-session state is a richer model
// now (EnvActivityPayload's aiState/aiTool/... fields, sourced from the tool's
// own structured report) and no longer goes through this event — an
// orchestrator has no pod to report through that path, so it keeps reporting
// its own turn boundaries directly.
export interface AIActivityPayload {
  sessionId: number;
  busy: boolean;
}
