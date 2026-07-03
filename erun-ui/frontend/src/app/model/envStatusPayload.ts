// EnvStatusPayload mirrors the Go-side env-status payload behind the sidebar's
// open dot. status is '' (healthy / open attempt in flight), 'stopped' (linked
// cloud context not running), or 'failed' (deploy failed or reconnect gave up).
export interface EnvStatusPayload {
  tenant: string;
  environment: string;
  status: string;
}
