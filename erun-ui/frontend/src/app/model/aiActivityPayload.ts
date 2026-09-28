// AIActivityPayload drives the sidebar's AI activity signals for an env row: a
// "working" spinner while its AI tab is producing output, and a "waiting on
// you" marker when the AI tool reported that control went back to the operator
// — the state a volume heuristic structurally cannot produce, since a session
// blocked on a human prints nothing.
//
// The two are one state, not two: the AI tool's own report always names both,
// so a consumer applies both from every event rather than only when the field
// it cares about changes. `awaitingInput` is absent on the volume fallback and
// on orchestrator sessions, which report turn boundaries through their own
// model and have no blocked-on-the-operator state to report.
export interface AIActivityPayload {
  sessionId: number;
  tenant: string;
  environment: string;
  busy: boolean;
  awaitingInput?: boolean;
}
