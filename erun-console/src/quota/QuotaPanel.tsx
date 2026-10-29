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

function tenantLabel(tenants: PlatformTenant[], tenantId: string | undefined): string | undefined {
  if (tenantId === undefined) {
    return undefined;
  }
  return tenants.find((tenant) => tenant.tenantId === tenantId)?.name;
}

// Read-only; quotas are set from the Tenants panel. A scoped read is badged so it is
// not mistaken for the caller's own.
export function QuotaPanel({
  token,
  tenants = [],
  scopeTenantId,
}: {
  token: string;
  // Populated only for an OPERATIONS caller.
  tenants?: PlatformTenant[];
  // Undefined means the caller's own tenant.
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
