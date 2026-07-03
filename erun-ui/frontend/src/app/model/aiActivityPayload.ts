// AIActivityPayload drives a "working" spinner on an env's sidebar row while its AI
// tab produces output, including when the user has navigated away to a different env.
export interface AIActivityPayload {
  sessionId: number;
  tenant: string;
  environment: string;
  busy: boolean;
}
