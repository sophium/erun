// SidebarFocus is the single source of row highlight, so at most one row is
// highlighted.
export type SidebarFocus =
  | { kind: 'dashboard'; tenant: string }
  | { kind: 'orchestrator'; sessionId: number }
  | { kind: 'environment'; tenant: string; environment: string }
  | { kind: 'none' };
