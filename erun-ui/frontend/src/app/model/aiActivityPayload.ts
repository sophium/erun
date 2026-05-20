// AIActivityPayload mirrors the Go-side aiActivityPayload emitted by
// streamSession's debounced AI-output tracker. The desktop sidebar uses
// it to render a "working" spinner on the env row when its AI tab is
// actively producing output, even when the user has navigated to a
// different env. See erun-ui/terminal_sessions.go: recordAIActivity.
export interface AIActivityPayload {
  sessionId: number;
  tenant: string;
  environment: string;
  busy: boolean;
}
