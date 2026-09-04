import type { TenantConfigView } from 'erun-kit';
import {
  Building2,
  Cloud,
  Inbox,
  KeyRound,
  LayoutDashboard,
  ListChecks,
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
  | 'gate-runs'
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
  // Requests is every tenant's own queue too: a COMPANY
  // tenant's admin needs to see and decide JOIN_TENANT requests naming their
  // own tenant, not just an OPERATIONS operator -- GET /v1/invite-requests is
  // TenantUserClass, and Approve/Decline are gated within the panel itself
  // on whoami's capability set rather than at the nav level (see
  // requests/RequestsPanel.tsx).
  { id: 'requests', label: 'Requests', icon: Inbox },
  // Gate runs (erun#1932) is every tenant's own view of the merge-queue gate:
  // what is being gated right now, and what recent gates decided (GET
  // /v1/gate-runs is TenantUserClass -- no tenant type restricts it).
  { id: 'gate-runs', label: 'Gate runs', icon: ListChecks },
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

// The full id set regardless of tenant type, for validating a URL-derived
// candidate before it is even checked against what the current tenant may
// see (sectionUrl.ts).
export const ALL_SECTION_IDS: ConsoleSectionId[] = [...BASE_SECTIONS, ...OPERATIONS_SECTIONS].map(
  (section) => section.id,
);

// The sections whose own read actually threads scopeTenantId server-side:
// overview's QuotaPanel, environments' EnvironmentsPanel/AISessionsPanel,
// and users' UsersPanel. Every other section reads only
// the caller's own tenant regardless of the scope selector's value --
// ScopeSelector.tsx uses this to stop claiming a reach it does not have on
// those sections, rather than leaving that gap unstated. Grow this set only
// alongside the panel that actually starts honoring scope; a section added
// here with no matching plumbing would make the selector lie the other way.
const SCOPE_AWARE_SECTIONS: ReadonlySet<ConsoleSectionId> = new Set<ConsoleSectionId>([
  'overview',
  'environments',
  'users',
]);

export function sectionHonorsScope(id: ConsoleSectionId): boolean {
  return SCOPE_AWARE_SECTIONS.has(id);
}
