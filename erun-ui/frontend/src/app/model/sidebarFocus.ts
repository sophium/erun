// SidebarFocus names the one thing that currently owns the terminal pane and
// the sidebar's highlight — the tenant dashboard, an orchestrator's session,
// or an environment's session. Every sidebar row derives its own highlight
// from this single value instead of computing an overlapping "active"
// condition from a different state slice, so at most one row is ever
// highlighted at a time.
export type SidebarFocus =
  | { kind: 'dashboard'; tenant: string }
  | { kind: 'orchestrator'; sessionId: number }
  | { kind: 'environment'; tenant: string; environment: string }
  | { kind: 'none' };
