import { Card, CardAction, CardContent, CardHeader, CardTitle, StatusBadge } from 'erun-kit';
import type * as React from 'react';

import type { TenantQuota } from '../app/api/quotaApi';
import type { PlatformTenant } from '../app/api/tenantsApi';
import type { QuotaState } from './controller';
import { useQuotaController } from './controller';

function QuotaRow({ label, value }: { label: string; value: string }): React.ReactElement {
  return (
    <div className="flex items-center justify-between border-b border-border py-2 text-sm last:border-b-0">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="font-medium text-foreground">{value}</dd>
    </div>
  );
}

function QuotaList({ quota }: { quota: TenantQuota }): React.ReactElement {
  return (
    <dl>
      <QuotaRow label="Environments" value={String(quota.maxEnvironments)} />
      <QuotaRow label="Per-environment CPU" value={`${String(quota.maxCpuMillicores)}m`} />
      <QuotaRow label="Per-environment memory" value={`${String(quota.maxMemoryMb)} MB`} />
      <QuotaRow label="Per-environment storage" value={`${String(quota.maxStorageGb)} GB`} />
      <QuotaRow label="Total CPU" value={`${String(quota.maxTotalCpuMillicores)}m`} />
      <QuotaRow label="Total memory" value={`${String(quota.maxTotalMemoryMb)} MB`} />
      <QuotaRow label="Total storage" value={`${String(quota.maxTotalStorageGb)} GB`} />
    </dl>
  );
}

function QuotaBody({ quotaState }: { quotaState: QuotaState }): React.ReactElement {
  if (quotaState.status === 'loading') {
    return (
      <p className="text-sm text-muted-foreground" role="status">
        Loading quota…
      </p>
    );
  }
  if (quotaState.status === 'error') {
    return (
      <p className="text-sm text-destructive" role="alert">
        Could not load quota: {quotaState.message}
      </p>
    );
  }
  return <QuotaList quota={quotaState.quota} />;
}

// tenantLabel looks up the scoped tenant's name for the panel's "whose quota
// is this" badge -- undefined for the caller's own tenant (no scope) or a
// tenant id the caller's own tenant list doesn't (yet) carry. Same shape as
// environments/EnvironmentsPanel.tsx's row-level equivalent.
function tenantLabel(tenants: PlatformTenant[], tenantId: string | undefined): string | undefined {
  if (tenantId === undefined) {
    return undefined;
  }
  return tenants.find((tenant) => tenant.tenantId === tenantId)?.name;
}

// QuotaPanel is every tenant's self-service view of its own caps — env count,
// the per-environment resource ceiling every provisioned environment gets,
// and the aggregate tenant-wide budget (see erun-backend-api/internal/routes/
// tenant_quotas.go's TenantQuotaReader). Read-only: only an operations tenant
// can change a quota, from the Tenants panel's per-tenant "Set quota" action.
// Wired to shell/ScopeSelector.tsx: an OPERATIONS caller pointed at another
// tenant sees that tenant's caps here too, named by the badge below rather
// than rendering unlabeled -- otherwise a scoped read is indistinguishable
// from the caller's own (erun#1816).
export function QuotaPanel({
  token,
  tenants = [],
  scopeTenantId,
}: {
  token: string;
  // The full tenant list, for naming the scoped tenant on the badge below --
  // populated only for an OPERATIONS caller (shell/AppShell.tsx).
  tenants?: PlatformTenant[];
  // Set once shell/ScopeSelector.tsx points this caller at another tenant;
  // undefined means "my own tenant", the default and every caller's ordinary
  // behavior before the scope selector existed.
  scopeTenantId?: string;
}): React.ReactElement {
  const quotaState = useQuotaController(token, scopeTenantId);
  const label = tenantLabel(tenants, scopeTenantId);
  return (
    <Card aria-labelledby="quota-heading">
      <CardHeader>
        <CardTitle id="quota-heading">Quota</CardTitle>
        {label !== undefined && (
          <CardAction>
            <StatusBadge tone="muted" label={label} showIcon={false} />
          </CardAction>
        )}
      </CardHeader>
      <CardContent>
        <QuotaBody quotaState={quotaState} />
      </CardContent>
    </Card>
  );
}
