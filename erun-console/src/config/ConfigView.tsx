import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  type CloudContext,
  type ContextStatus,
  EmptyState,
  type Environment,
  type EnvironmentStatus,
  StatusBadge,
  type StatusBadgeTone,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
  type Tenant,
  type TenantConfigView,
} from 'erun-kit';
import { Cloud, Server } from 'lucide-react';
import type * as React from 'react';

// Empty collections render an empty-state card, never an empty table.

function placeholder(value: string | undefined): string {
  return value && value.length > 0 ? value : '—';
}

// Always `alert`: both call sites render it only for a failed status, and the reason
// appears after the badge changes, so it must be announced.
function ProvisionFailure({ message }: { message: string }): React.ReactElement {
  return (
    <span className="text-xs text-destructive" role="alert" aria-live="polite">
      {message}
    </span>
  );
}

function TenantHeader({ tenant }: { tenant: Tenant }): React.ReactElement {
  return (
    <header>
      <h2 className="text-xl font-semibold text-foreground">{tenant.name}</h2>
      <p className="mt-1 text-sm text-muted-foreground">Tenant · {tenant.type || 'unknown type'}</p>
    </header>
  );
}

// The version the last successful deploy installed, distinct from the declared pin.
function deployedVersionCell(env: Environment): string {
  if (env.deployedVersion === undefined) {
    return '—';
  }
  return env.deployedVersion !== env.runtimeVersion
    ? `${env.deployedVersion} (pin: ${placeholder(env.runtimeVersion)})`
    : env.deployedVersion;
}

function EnvironmentRow({ env }: { env: Environment }): React.ReactElement {
  return (
    <TableRow>
      <TableCell className="font-medium text-foreground">{env.name}</TableCell>
      <TableCell>{env.type}</TableCell>
      <TableCell>{placeholder(env.kubernetesContext)}</TableCell>
      <TableCell>{placeholder(env.runtimeVersion)}</TableCell>
      <TableCell>{deployedVersionCell(env)}</TableCell>
      <TableCell>
        <div className="flex flex-col items-start gap-1">
          {renderStatusBadge(env.status, ENV_STATUS_TONES, ENV_STATUS_LABELS)}
          {env.status === 'failed' && env.provisionError !== undefined && (
            <ProvisionFailure message={env.provisionError} />
          )}
        </div>
      </TableCell>
    </TableRow>
  );
}

function EnvironmentsSection({
  environments,
}: {
  environments: Environment[];
}): React.ReactElement {
  return (
    <Card role="region" aria-labelledby="environments-heading">
      <CardHeader>
        <CardTitle id="environments-heading">Environments</CardTitle>
      </CardHeader>
      <CardContent>
        {environments.length === 0 ? (
          <EmptyState icon={<Server />} heading="No environments yet." />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>Kubernetes context</TableHead>
                <TableHead>Runtime version</TableHead>
                <TableHead>Deployed version</TableHead>
                <TableHead>Status</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {environments.map((env) => (
                <EnvironmentRow key={env.environmentId} env={env} />
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
}

const STATUS_LABELS: Record<ContextStatus, string> = {
  provisioning: 'Provisioning',
  running: 'Running',
  failed: 'Failed',
};

const STATUS_TONES: Record<ContextStatus, StatusBadgeTone> = {
  provisioning: 'in-progress',
  running: 'success',
  failed: 'destructive',
};

const ENV_STATUS_LABELS: Record<EnvironmentStatus, string> = {
  registered: 'Registered',
  provisioning: 'Provisioning',
  running: 'Running',
  failed: 'Failed',
  // Without these the lenient status parse renders no badge for a teardown.
  deleting: 'Deleting',
  'deletion-blocked': 'Delete blocked',
};

const ENV_STATUS_TONES: Record<EnvironmentStatus, StatusBadgeTone> = {
  registered: 'muted',
  provisioning: 'in-progress',
  running: 'success',
  failed: 'destructive',
  deleting: 'in-progress',
  'deletion-blocked': 'warning',
};

// Shares erun-kit's StatusBadge with the desktop; no status renders no badge.
function renderStatusBadge<T extends string>(
  status: T | undefined,
  tones: Record<T, StatusBadgeTone>,
  labels: Record<T, string>,
): React.ReactElement | null {
  if (status === undefined) {
    return null;
  }
  return <StatusBadge tone={tones[status]} label={labels[status]} />;
}

function ContextItem({ context }: { context: CloudContext }): React.ReactElement {
  return (
    <li className="flex items-center justify-between gap-3 border-b border-border py-2.5 text-sm last:border-b-0">
      <span className="font-medium text-foreground">{context.name}</span>
      <span className="text-muted-foreground">
        {context.provider} · {context.region}
      </span>
      <span className="flex flex-col items-end gap-1">
        {renderStatusBadge(context.status, STATUS_TONES, STATUS_LABELS)}
        {context.status === 'failed' && context.provisionError !== undefined && (
          <ProvisionFailure message={context.provisionError} />
        )}
      </span>
    </li>
  );
}

function ContextsSection({ contexts }: { contexts: CloudContext[] }): React.ReactElement {
  return (
    <Card role="region" aria-labelledby="contexts-heading">
      <CardHeader>
        <CardTitle id="contexts-heading">Cloud contexts</CardTitle>
      </CardHeader>
      <CardContent>
        {contexts.length === 0 ? (
          <EmptyState icon={<Cloud />} heading="No cloud contexts yet." />
        ) : (
          <ul>
            {contexts.map((context) => (
              <ContextItem key={context.contextId} context={context} />
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}

export function ConfigView({ config }: { config: TenantConfigView }): React.ReactElement {
  return (
    <div className="grid gap-6">
      <TenantHeader tenant={config.tenant} />
      <EnvironmentsSection environments={config.environments} />
      <ContextsSection contexts={config.contexts} />
    </div>
  );
}
