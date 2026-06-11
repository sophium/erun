// EnvStatusPayload mirrors the Go-side envStatusPayload emitted by the
// env-status Wails event (issue #470): the real per-env condition behind the
// sidebar's open dot. status is '' (healthy / fresh open attempt in flight),
// 'stopped' (linked cloud context not running), or 'failed' (deploy failed or
// reconnect gave up).
export interface EnvStatusPayload {
  tenant: string;
  environment: string;
  status: string;
}
