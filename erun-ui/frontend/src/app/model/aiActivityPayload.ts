// Drives an env's sidebar spinner while its AI tab produces output, even when another env is selected.
export interface AIActivityPayload {
  sessionId: number;
  tenant: string;
  environment: string;
  busy: boolean;
}
