import type { TenantConfigView } from 'erun-kit';
import {
  Building2,
  Cloud,
  Inbox,
  KeyRound,
  LayoutDashboard,
  Mail,
  Server,
  Settings,
  UserPlus,
  Users,
} from 'lucide-react';

// Identity administration (issue #1209) is restricted server-side to an
// OPERATIONS tenant; gating the nav entries here too keeps a COMPANY-tenant
// operator from ever seeing an entry whose panel would just come back 403.
export const OPERATIONS_TENANT_TYPE = 'OPERATIONS';

export type ConsoleSectionId =
  | 'overview'
  | 'environments'
  | 'provisioning'
  | 'mcp-access'
  | 'invites'
  | 'requests'
  | 'tenants'
  | 'users'
  | 'org-settings'
  | 'smtp-settings';

export interface ConsoleSection {
  id: ConsoleSectionId;
  label: string;
  icon: typeof LayoutDashboard;
}

const BASE_SECTIONS: ConsoleSection[] = [
  { id: 'overview', label: 'Overview', icon: LayoutDashboard },
  { id: 'environments', label: 'Environments', icon: Server },
  { id: 'provisioning', label: 'Cloud contexts', icon: Cloud },
  { id: 'mcp-access', label: 'MCP access', icon: KeyRound },
  // Unlike the OPERATIONS-only sections below, invites (#1483) are every
  // tenant's own way to add members now that self-registration is closed
  // (#1482) — a COMPANY tenant needs this exactly as much as an OPERATIONS
  // one, so it belongs in the base set every tenant type sees.
  { id: 'invites', label: 'Invites', icon: UserPlus },
  // Requests (issue #1682) is every tenant's own queue too: a COMPANY
  // tenant's admin needs to see and decide JOIN_TENANT requests naming their
  // own tenant, not just an OPERATIONS operator -- GET /v1/invite-requests is
  // TenantUserClass, and Approve/Decline are gated within the panel itself
  // on whoami's capability set rather than at the nav level (see
  // requests/RequestsPanel.tsx).
  { id: 'requests', label: 'Requests', icon: Inbox },
];

const OPERATIONS_SECTIONS: ConsoleSection[] = [
  // Tenants is the one action only an OPERATIONS tenant can take —
  // registering a new tenant — so it leads the operations-only group.
  { id: 'tenants', label: 'Tenants', icon: Building2 },
  { id: 'users', label: 'Users', icon: Users },
  { id: 'org-settings', label: 'Org settings', icon: Settings },
  { id: 'smtp-settings', label: 'Outbound mail', icon: Mail },
];

// The nav mirrors the backend's own OPERATIONS-only restriction on identity
// administration, so a COMPANY tenant never sees the entries at all — not
// just a panel that would 403 after the click.
export function sectionsForTenant(tenant: TenantConfigView['tenant']): ConsoleSection[] {
  return tenant.type === OPERATIONS_TENANT_TYPE
    ? [...BASE_SECTIONS, ...OPERATIONS_SECTIONS]
    : BASE_SECTIONS;
}
