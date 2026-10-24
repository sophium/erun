import type { TenantConfigView } from 'erun-kit';
import * as React from 'react';

import { PENDING_REQUESTS_POLL_MS, useListInviteRequestsQuery } from '../app/api/requestsApi';
import { type PlatformTenant, useListTenantsQuery } from '../app/api/tenantsApi';
import { useGetWhoamiQuery } from '../app/api/whoamiApi';
import type { OidcConfig } from '../auth/auth';
import { readTokenIdentity } from '../auth/identity';
import { ConfigView } from '../config/ConfigView';
import { AISessionsPanel } from '../environments/AISessionsPanel';
import { EnvironmentsPanel } from '../environments/EnvironmentsPanel';
import { InvitesPanel } from '../identity/InvitesPanel';
import { OrgSettingsPanel } from '../identity/OrgSettingsPanel';
import { SmtpSettingsPanel } from '../identity/SmtpSettingsPanel';
import { UsersPanel } from '../identity/UsersPanel';
import { JobsPanel } from '../jobs/JobsPanel';
import { MCPAccessPanel } from '../mcp/MCPAccessPanel';
import { PipelinePanel } from '../pipeline/PipelinePanel';
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

// Split from SectionContent to stay under the eslint `complexity` budget.
function OperationsSectionContent({
  active,
  token,
  docsUrl,
  tenant,
  callerErunUserId,
  scopeTenantId,
}: {
  active: 'tenants' | 'users' | 'org-settings' | 'smtp-settings';
  token: string;
  docsUrl: string | undefined;
  tenant: TenantConfigView['tenant'];
  callerErunUserId: string | undefined;
  scopeTenantId: string | undefined;
}): React.ReactElement {
  switch (active) {
    case 'tenants':
      return <TenantsPanel token={token} docsUrl={docsUrl} />;
    case 'users':
      return (
        <UsersPanel
          token={token}
          ownTenantId={tenant.tenantId}
          tenantType={tenant.type}
          callerErunUserId={callerErunUserId}
          scopeTenantId={scopeTenantId}
        />
      );
    case 'org-settings':
      return <OrgSettingsPanel token={token} />;
    case 'smtp-settings':
      return <SmtpSettingsPanel token={token} />;
  }
}

function SectionContent({
  active,
  token,
  config,
  docsUrl,
  tenants,
  scopeTenantId,
  callerErunUserId,
  onChanged,
}: {
  active: ConsoleSectionId;
  token: string;
  config: TenantConfigView;
  docsUrl: string | undefined;
  tenants: PlatformTenant[];
  scopeTenantId: string | undefined;
  callerErunUserId: string | undefined;
  onChanged: () => void;
}): React.ReactElement {
  switch (active) {
    case 'overview':
      return (
        <div className="grid gap-6">
          <ConfigView config={config} />
          <QuotaPanel token={token} tenants={tenants} scopeTenantId={scopeTenantId} />
        </div>
      );
    case 'environments':
      return (
        <div className="grid gap-6">
          <EnvironmentsPanel
            token={token}
            contexts={config.contexts}
            environments={config.environments}
            tenants={tenants}
            scopeTenantId={scopeTenantId}
            onChanged={onChanged}
          />
          <AISessionsPanel
            token={token}
            environments={config.environments}
            scopeTenantId={scopeTenantId}
          />
        </div>
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
    case 'jobs':
      return <JobsPanel token={token} />;
    case 'pipeline':
      return <PipelinePanel token={token} />;
    default:
      return (
        <OperationsSectionContent
          active={active}
          token={token}
          docsUrl={docsUrl}
          tenant={config.tenant}
          callerErunUserId={callerErunUserId}
          scopeTenantId={scopeTenantId}
        />
      );
  }
}

function sectionLabel(sections: ConsoleSection[], active: ConsoleSectionId): string {
  return sections.find((section) => section.id === active)?.label ?? '';
}

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
  // Local state: scope need not survive this signed-in session.
  const [scopeTenantId, setScopeTenantId] = React.useState<string | undefined>(undefined);
  // Skipped for a non-OPERATIONS caller, who has no scope to administer.
  const tenantsQuery = useListTenantsQuery(token, {
    skip: config.tenant.type !== 'OPERATIONS',
  });
  const tenants = tenantsQuery.data ?? [];
  // Never fall back to the raw `sub` claim: whoami's username, then the token email,
  // else "not resolved".
  const identity = React.useMemo(() => readTokenIdentity(token), [token]);
  const whoamiQuery = useGetWhoamiQuery(token);
  const identityLabel = whoamiQuery.data?.username ?? identity.email;
  const identityPending =
    identityLabel === undefined && (whoamiQuery.isLoading || whoamiQuery.isUninitialized);

  // Shares the panel's cached query, so the nav badge costs no extra request.
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
            callerErunUserId={whoamiQuery.data?.userId}
            onChanged={onChanged}
          />
        </main>
      </div>
    </div>
  );
}
