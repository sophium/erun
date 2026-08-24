import { Cloud, KeyRound, LayoutDashboard, Server, Settings, Users } from 'lucide-react';

import type { TenantConfigView } from '../config/types';

// Identity administration (issue #1209) is restricted server-side to an
// OPERATIONS tenant; gating the nav entries here too keeps a COMPANY-tenant
// operator from ever seeing an entry whose panel would just come back 403.
export const OPERATIONS_TENANT_TYPE = 'OPERATIONS';

export type ConsoleSectionId =
  | 'overview'
  | 'environments'
  | 'provisioning'
  | 'mcp-access'
  | 'users'
  | 'org-settings';

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
];

const OPERATIONS_SECTIONS: ConsoleSection[] = [
  { id: 'users', label: 'Users', icon: Users },
  { id: 'org-settings', label: 'Org settings', icon: Settings },
];

// The nav mirrors the backend's own OPERATIONS-only restriction on identity
// administration, so a COMPANY tenant never sees the entries at all — not
// just a panel that would 403 after the click.
export function sectionsForTenant(tenant: TenantConfigView['tenant']): ConsoleSection[] {
  return tenant.type === OPERATIONS_TENANT_TYPE
    ? [...BASE_SECTIONS, ...OPERATIONS_SECTIONS]
    : BASE_SECTIONS;
}
