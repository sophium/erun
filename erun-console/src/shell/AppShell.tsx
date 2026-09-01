import type { TenantConfigView } from 'erun-kit';
import * as React from 'react';

import { PENDING_REQUESTS_POLL_MS, useListInviteRequestsQuery } from '../app/api/requestsApi';
import { type PlatformTenant, useListTenantsQuery } from '../app/api/tenantsApi';
import { useGetWhoamiQuery } from '../app/api/whoamiApi';
import type { OidcConfig } from '../auth/auth';
import { readTokenIdentity } from '../auth/identity';
import { ConfigView } from '../config/ConfigView';
import { EnvironmentsPanel } from '../environments/EnvironmentsPanel';
import { InvitesPanel } from '../identity/InvitesPanel';
import { OrgSettingsPanel } from '../identity/OrgSettingsPanel';
import { SmtpSettingsPanel } from '../identity/SmtpSettingsPanel';
import { UsersPanel } from '../identity/UsersPanel';
import { MCPAccessPanel } from '../mcp/MCPAccessPanel';
import { ProvisionPanel } from '../provision/ProvisionPanel';
import { QuotaPanel } from '../quota/QuotaPanel';
import { RequestsPanel } from '../requests/RequestsPanel';
import { TenantsPanel } from '../tenants/TenantsPanel';
import { ConsoleHeader } from './ConsoleHeader';
import { ConsoleSidebar } from './ConsoleSidebar';
import type { ConsoleSection, ConsoleSectionId } from './sections';
import {
  type TenantSwitchMismatch,
  TenantSwitchMismatchBanner,
} from './TenantSwitchMismatchBanner';
import { useSectionNavigation } from './useSectionNavigation';
import { useTheme } from './useTheme';

// OperationsSectionContent renders the operations-only identity/tenant
// sections -- split out of SectionContent below purely to keep either
// function's switch under the module's complexity budget as sections keep
// being added (each case is its own branch for eslint's `complexity` rule).
function OperationsSectionContent({
  active,
  token,
  docsUrl,
  tenant,
}: {
  active: 'tenants' | 'users' | 'org-settings' | 'smtp-settings';
  token: string;
  docsUrl: string | undefined;
  tenant: TenantConfigView['tenant'];
}): React.ReactElement {
  switch (active) {
    case 'tenants':
      return <TenantsPanel token={token} docsUrl={docsUrl} />;
    case 'users':
      return <UsersPanel token={token} ownTenantId={tenant.tenantId} tenantType={tenant.type} />;
    case 'org-settings':
      return <OrgSettingsPanel token={token} />;
    case 'smtp-settings':
      return <SmtpSettingsPanel token={token} />;
  }
}

// SectionContent renders exactly one panel for the active section — the main
// pane switches wholesale rather than stacking every panel, per #1207. Split
// out of AppShell purely to keep AppShell under the module's
// max-lines-per-function budget.
function SectionContent({
  active,
  token,
  config,
  docsUrl,
  tenants,
  scopeTenantId,
  onChanged,
}: {
  active: ConsoleSectionId;
  token: string;
  config: TenantConfigView;
  docsUrl: string | undefined;
  tenants: PlatformTenant[];
  scopeTenantId: string | undefined;
  onChanged: () => void;
}): React.ReactElement {
  switch (active) {
    case 'overview':
      return (
        <div className="grid gap-6">
          <ConfigView config={config} />
          <QuotaPanel token={token} />
        </div>
      );
    case 'environments':
      return (
        <EnvironmentsPanel
          token={token}
          contexts={config.contexts}
          environments={config.environments}
          tenants={tenants}
          scopeTenantId={scopeTenantId}
          onChanged={onChanged}
        />
      );
    case 'provisioning':
      return <ProvisionPanel token={token} />;
    case 'mcp-access':
      return <MCPAccessPanel token={token} environments={config.environments} />;
    case 'invites':
      return <InvitesPanel token={token} />;
    case 'requests':
      return (
        <RequestsPanel
          token={token}
          tenantType={config.tenant.type}
          rateLimitWindowSeconds={config.inviteRequestRateLimitWindowSeconds}
        />
      );
    default:
      return (
        <OperationsSectionContent
          active={active}
          token={token}
          docsUrl={docsUrl}
          tenant={config.tenant}
        />
      );
  }
}

function sectionLabel(sections: ConsoleSection[], active: ConsoleSectionId): string {
  return sections.find((section) => section.id === active)?.label ?? '';
}

// AppShell is the console's app shell (#1207): a persistent sidebar for
// navigation, a header carrying identity + sign-out, and a main pane that
// switches on a single derived `active` value rather than stacking every
// panel vertically.
export function AppShell({
  brand,
  token,
  config,
  docsUrl,
  oidc,
  switchMismatch,
  onRetrySwitch,
  onDismissSwitchMismatch,
  onChanged,
  onSignOut,
}: {
  brand: string | undefined;
  token: string;
  config: TenantConfigView;
  docsUrl?: string;
  oidc: OidcConfig | undefined;
  switchMismatch: TenantSwitchMismatch | undefined;
  onRetrySwitch?: () => void;
  onDismissSwitchMismatch: () => void;
  onChanged: () => void;
  onSignOut: () => void;
}): React.ReactElement {
  const { sections, active, onSelect } = useSectionNavigation(config.tenant);
  const { theme, toggleTheme } = useTheme();
  // scopeTenantId is undefined for "my own tenant" -- the default, and every
  // caller's ordinary behavior before the scope selector existed. Local state
  // rather than a store slice: it has no reason to survive past this signed-in
  // session (shell/ScopeSelector.tsx, erun#1816).
  const [scopeTenantId, setScopeTenantId] = React.useState<string | undefined>(undefined);
  // The full tenant list backs both the scope selector's options and the
  // Environments panel's per-row tenant badge; skipped for a non-OPERATIONS
  // caller, who has no scope to administer and only ever sees their own rows.
  const tenantsQuery = useListTenantsQuery(token, {
    skip: config.tenant.type !== 'OPERATIONS',
  });
  const tenants = tenantsQuery.data ?? [];
  // The header must never fall back to the raw `sub` claim -- whoami's own
  // `username` is the platform's authoritative label for the enrolled user,
  // and the token-claim email is a reasonable interim label while that query
  // is still resolving. Neither present yet means "not resolved", shown as
  // such rather than silently substituting the opaque subject id.
  const identity = React.useMemo(() => readTokenIdentity(token), [token]);
  const whoamiQuery = useGetWhoamiQuery(token);
  const identityLabel = whoamiQuery.data?.username ?? identity.email;
  const identityPending =
    identityLabel === undefined && (whoamiQuery.isLoading || whoamiQuery.isUninitialized);

  // The pending-request count has to be visible without opening the
  // Requests panel, so it's read here rather than only inside RequestsPanel
  // -- the same query, shared
  // through RTK Query's cache, so this costs no extra request beyond the
  // panel's own poll once both are subscribed.
  const pendingRequestsQuery = useListInviteRequestsQuery(token, {
    pollingInterval: PENDING_REQUESTS_POLL_MS,
  });
  const counts = React.useMemo<Partial<Record<ConsoleSectionId, number>>>(
    () => ({ requests: pendingRequestsQuery.data?.length }),
    [pendingRequestsQuery.data],
  );

  return (
    <div className="flex h-dvh min-h-0 w-full bg-background text-foreground">
      <ConsoleSidebar
        brand={brand}
        token={token}
        currentTenant={config.tenant}
        oidc={oidc}
        sections={sections}
        active={active}
        counts={counts}
        onSelect={onSelect}
        tenants={tenants}
        scopeTenantId={scopeTenantId}
        onScopeChange={setScopeTenantId}
      />
      <div className="flex min-w-0 flex-1 flex-col">
        <ConsoleHeader
          title={sectionLabel(sections, active)}
          identityLabel={identityLabel}
          identityPending={identityPending}
          theme={theme}
          onToggleTheme={toggleTheme}
          onSignOut={onSignOut}
        />
        {switchMismatch !== undefined && (
          <TenantSwitchMismatchBanner
            mismatch={switchMismatch}
            onRetry={onRetrySwitch}
            onDismiss={onDismissSwitchMismatch}
          />
        )}
        <main className="min-h-0 flex-1 overflow-auto px-6 py-6">
          <SectionContent
            active={active}
            token={token}
            config={config}
            docsUrl={docsUrl}
            tenants={tenants}
            scopeTenantId={scopeTenantId}
            onChanged={onChanged}
          />
        </main>
      </div>
    </div>
  );
}
