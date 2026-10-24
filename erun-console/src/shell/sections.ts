import type { TenantConfigView } from 'erun-kit';
import {
  Activity,
  Building2,
  Cloud,
  Inbox,
  KeyRound,
  LayoutDashboard,
  ListTree,
  Mail,
  Server,
  Settings,
  UserPlus,
  Users,
} from 'lucide-react';

// Identity administration is OPERATIONS-only server-side; the nav mirrors it.
export const OPERATIONS_TENANT_TYPE = 'OPERATIONS';

export type ConsoleSectionId =
  | 'overview'
  | 'environments'
  | 'provisioning'
  | 'mcp-access'
  | 'invites'
  | 'requests'
  | 'jobs'
  | 'pipeline'
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
  { id: 'invites', label: 'Invites', icon: UserPlus },
  // Approve/Decline are gated inside RequestsPanel on whoami capabilities.
  { id: 'requests', label: 'Requests', icon: Inbox },
  // Jobs and Pipeline read only the caller's own tenant, so they are not scope-aware.
  { id: 'jobs', label: 'Jobs', icon: Activity },
  { id: 'pipeline', label: 'Pipeline', icon: ListTree },
];

const OPERATIONS_SECTIONS: ConsoleSection[] = [
  { id: 'tenants', label: 'Tenants', icon: Building2 },
  { id: 'users', label: 'Users', icon: Users },
  { id: 'org-settings', label: 'Org settings', icon: Settings },
  { id: 'smtp-settings', label: 'Outbound mail', icon: Mail },
];

export function sectionsForTenant(tenant: TenantConfigView['tenant']): ConsoleSection[] {
  return tenant.type === OPERATIONS_TENANT_TYPE
    ? [...BASE_SECTIONS, ...OPERATIONS_SECTIONS]
    : BASE_SECTIONS;
}

// ALL_SECTION_IDS validates a URL-derived candidate before permission checks.
export const ALL_SECTION_IDS: ConsoleSectionId[] = [...BASE_SECTIONS, ...OPERATIONS_SECTIONS].map(
  (section) => section.id,
);

// Sections whose reads thread scopeTenantId server-side. Add one only with
// the panel plumbing, or the scope selector will misreport its reach.
const SCOPE_AWARE_SECTIONS: ReadonlySet<ConsoleSectionId> = new Set<ConsoleSectionId>([
  'overview',
  'environments',
  'users',
]);

export function sectionHonorsScope(id: ConsoleSectionId): boolean {
  return SCOPE_AWARE_SECTIONS.has(id);
}
